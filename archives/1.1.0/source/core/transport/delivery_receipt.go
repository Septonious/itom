package transport

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// A receipt is emitted only for a complete authenticated SERVER body after
// every record has applied/discarded. It consumes bounded existing metadata,
// never another message or grant. It is NOT deletion/absence proof, a logical
// DATA receipt, or a measurement of RTT/useful bandwidth.
type deliveryReceipt struct {
	uid     uint32
	keyword string
	command *Command
}

type DeliveryReceiptStats struct {
	Queued, Marked, Refused uint64
	Pending                 int
	Disabled                bool
}

func receiptKeyword(body []byte, validity, uid uint32, key []byte) string {
	digest := sha256.Sum256(body)
	m := hmac.New(sha256.New, key)
	m.Write([]byte("ITOM/session-v2/received-body/v1\x00"))
	m.Write(digest[:]) // includes original store/run/session/account/folder/batch
	var location [8]byte
	binary.BigEndian.PutUint32(location[:4], validity)
	binary.BigEndian.PutUint32(location[4:], uid)
	m.Write(location[:])
	return "ITOM2R_" + hex.EncodeToString(m.Sum(nil))
}

// EnableDeliveryReceipts opts a client inbox into peer delivery metadata.
// The issuer still needs its original observed BODY and same-incarnation
// post-expunge absence proof before it can release physical journal debt.
func (i *Inbox) EnableDeliveryReceipts() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.server || i.session == nil || i.admit == nil {
		return ErrProtocol
	}
	i.session.mu.Lock()
	defer i.session.mu.Unlock()
	if i.session.services != 0 || i.session.runtime != nil {
		return ErrProtocol
	}
	if !i.executor.keywords {
		return ErrKeywordsUnsupported
	}
	if i.receipts == nil {
		i.receipts = make(map[uint32]*deliveryReceipt)
	}
	i.ensureMaintenanceWake()
	i.receiptEnabled = true
	return nil
}

func (i *Inbox) DeliveryReceiptStats() DeliveryReceiptStats {
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.receiptStats
	s.Pending = len(i.receipts)
	return s
}

func (i *Inbox) markReceiptOne(ctx context.Context) (bool, error) {
	i.maintenanceMu.Lock()
	defer i.maintenanceMu.Unlock()
	i.mu.Lock()
	if !i.receiptEnabled || len(i.receipts) == 0 {
		i.mu.Unlock()
		return false, nil
	}
	var uid uint32
	for next := range i.receipts {
		if uid == 0 || next < uid {
			uid = next
		}
	}
	r := i.receipts[uid]
	i.mu.Unlock()
	if r.command == nil {
		var err error
		r.command, err = i.executor.StoreKeyword(ctx, uid, r.keyword, true)
		if err != nil {
			return false, err
		}
	}
	if _, err := r.command.Wait(ctx); err != nil {
		if errors.Is(err, ErrRefused) {
			i.mu.Lock()
			i.receiptEnabled = false
			i.receiptStats.Refused++
			i.receiptStats.Disabled = true
			i.mu.Unlock()
			return false, nil
		}
		return false, err
	}
	i.mu.Lock()
	delete(i.receipts, uid)
	i.receiptStats.Marked++
	i.mu.Unlock()
	return true, nil
}

// The short maintenance-state lock is held when queuing. Wakes are bounded hints, not authority.
func (i *Inbox) ensureMaintenanceWake() {
	if i.maintenanceChanged == nil {
		i.maintenanceChanged = make(chan struct{}, 1)
	}
}
func (i *Inbox) signalMaintenance() {
	select {
	case i.maintenanceChanged <- struct{}{}:
	default:
	}
}
