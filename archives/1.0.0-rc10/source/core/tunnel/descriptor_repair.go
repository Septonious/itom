package tunnel

import (
	"time"

	"itom.local/core/journal"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type descriptorRepair struct {
	next  uint64
	after time.Time
}

// UID visibility can reorder COORDINATOR_ACK and old wallet reconciliation.
// These controls are durably repeated by the sender until GC_STATUS/retirement
// confirms them. Defer without a quota acknowledgement or accounting mutation;
// no unbounded pre-bootstrap queue and no fresh-grant dependency/deadlock.
func (l *lane) deferBootstrapReconciliation(f wire.Frame) (bool, error) {
	if !l.side || f.Stream != 0 {
		return false, nil
	}
	var key wire.QuotaKey
	c, err := wire.DecodeControl(f)
	if err != nil {
		return false, err
	}
	switch m := c.(type) {
	case wire.UsageReport:
		key = m.Key
	case wire.StorageRelease:
		key = m.Key
	case wire.Retirement:
		if m.Ack {
			return false, journal.ErrScope
		}
		key = m.Key
	default:
		return false, nil
	}
	if key.User != l.user.ID || key.Account != l.account.ID {
		return false, journal.ErrScope
	}
	return true, nil
}

// The durable journal still rejects gaps. An authenticated forward gap is a
// discovery/reordering event: request the sender's durable prefix, without
// registering the future descriptor, refunding debt, or retrying DATA APPEND.
func (l *lane) repairDescriptorGap(m wire.UsageReport) (bool, error) {
	if m.Attempt == nil || m.Key.Account != l.account.ID || m.Key.User != l.user.ID {
		return false, nil
	}
	ref := journal.GrantRef{Store: m.Key.Store, ID: m.Key.Grant, Scope: journal.Scope{Account: m.Key.Account, User: m.Key.User, Session: m.Key.Session}}
	g, err := l.data.GetGrant(ref)
	if err != nil {
		return false, err
	}
	if !g.Remote || g.Run != m.Key.Epoch || m.Total.Messages <= g.Registered.Messages+1 || m.Total.Bytes <= g.Registered.Bytes || m.Total.Bytes > g.Limit.Bytes || m.Total.Messages > g.Limit.Messages || m.Attempt.EncodedBytes > m.Total.Bytes-g.Registered.Bytes {
		return false, nil
	}
	next := g.Registered.Messages + 1
	now := time.Now()
	if l.descriptorRepairs == nil {
		l.descriptorRepairs = make(map[wire.QuotaKey]descriptorRepair)
	}
	if prev, ok := l.descriptorRepairs[m.Key]; ok && prev.next == next && now.Before(prev.after) {
		return true, nil
	}
	if len(l.descriptorRepairs) >= wallet.MaxGrants {
		// This is a bounded traffic throttle, never an accounting record.
		for key, old := range l.descriptorRepairs {
			if !now.Before(old.after) {
				delete(l.descriptorRepairs, key)
			}
		}
		if len(l.descriptorRepairs) >= wallet.MaxGrants {
			return false, journal.ErrLimit
		}
	}
	if err := l.controlSend(wire.DescriptorRequest{Key: m.Key, Next: next}); err != nil {
		return true, err
	}
	l.descriptorRepairs[m.Key] = descriptorRepair{next: next, after: now.Add(time.Second)}
	if l.diag != nil {
		l.diag.event("event=descriptor_gap_repair")
	}
	return true, nil
}

func (l *lane) replayDescriptors(m wire.DescriptorRequest) error {
	if l.side || l.wallet == nil || m.Key.Account != l.account.ID || m.Key.User != l.user.ID || m.Next == 0 {
		return journal.ErrScope
	}
	entries, err := l.wallet.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Offer.Key != m.Key {
			continue
		}
		// Only durable descriptors are resent, always with freshly sealed control
		// records. Nothing here spends quota or resubmits a physical DATA message.
		for _, report := range entry.Pending {
			if report.Total.Messages >= m.Next {
				if err := l.controlSend(report); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return nil // A delayed request may name an already retired wallet entry.
}
