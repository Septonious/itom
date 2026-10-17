package tunnel

import (
	"context"
	"errors"
	"itom.local/core/datapath"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"sort"
	"time"
)

// Each S2C record has two authenticated headers, its DATA frame and the tag;
// ITD3 adds a 24-byte header and one length word per member. No nonce is spent.
func localBatchMIMESize(jobs []*datapath.Job) (uint64, error) {
	if len(jobs) < 1 || len(jobs) > wire.MaxDataBatchRecords {
		return 0, wire.ErrLimit
	}
	body := 24
	for _, job := range jobs {
		if job == nil || job.Attempt().PayloadBytes > wire.MaxData {
			return 0, wire.ErrLimit
		}
		body += 4 + 2*wire.HeaderSize + wire.FrameHeaderSize + wire.TagSize + int(job.Attempt().PayloadBytes)
	}
	size, err := mailbox.EncodedSize(body)
	if err == nil && size > dataBatchMIMEHardCap {
		err = mailbox.ErrLimit
	}
	return size, err
}

// Select and reserve together: a full account must not strand a logical batch
// while another account has proven capacity. The original per-origin headroom
// and physical journal limits remain authoritative in tryLocalGrant.
func (g *stripeGroup) takeLocalGrantForClass(ctx context.Context, avoid *lane, fast bool, bytes uint64) (*lane, journal.Grant, error) {
	return g.takeLocalGrantPreferred(ctx, avoid, nil, fast, bytes)
}

func (g *stripeGroup) takeLocalGrantPreferred(ctx context.Context, avoid, preferred *lane, fast bool, bytes uint64) (*lane, journal.Grant, error) {
	var grace <-chan time.Time
	if fast {
		g.mu.Lock()
		budget := g.fastSlotGraceLocked()
		g.mu.Unlock()
		timer := time.NewTimer(budget)
		defer timer.Stop()
		grace = timer.C
	}
	fallback := false
	for {
		peerWait := time.Duration(0)
		probeBlocked := false
		capacityWake := g.capacityNotice.watch()
		if err := ctx.Err(); err != nil {
			return nil, journal.Grant{}, err
		}
		g.mu.Lock()
		reservedReader := g.interactiveReadersLocked()
		fastCeiling := int64(1 << 62)
		if fast {
			fastCeiling = g.fastLatencyCeilingLocked()
		}
		keys := make([]string, 0, len(g.lanes))
		for key := range g.lanes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if fast {
			g.prioritizeFastTargetsLocked(keys, avoid)
			g.preferShortTargetLocked(keys, preferred)
		} else {
			g.prioritizeBulkTargetsLocked(keys)
		}
		if len(keys) > 0 {
			start := 0
			if fast {
				start = 0
			}
			for pass := 0; pass < 2; pass++ {
				for off := 0; off < len(keys); off++ {
					idx := (start + off) % len(keys)
					l := g.lanes[keys[idx]]
					if !fast && (l == reservedReader[0] || l == reservedReader[1]) {
						continue
					}
					if l == nil || !l.streamCarrierReady() || l.ctx.Err() != nil || l.physicalSlots == nil {
						continue
					}
					if fast && (l.controlDebt.Load() >= 80 || l.cleanupBlocksFast()) {
						continue
					}
					if fast && !fallback && g.carrierLatencyLocked(l) > fastCeiling {
						continue
					}
					if !fast {
						if full, wait := l.peerReadWindowWait(); full {
							probeBlocked = probeBlocked || wait == 0
							if wait > 0 && (peerWait == 0 || wait < peerWait) {
								peerWait = wait
							}
							continue
						}
					}
					if !fast && l.bulkControlBlocked.Load() {
						continue
					}
					if !fast && pass == 0 && avoid != nil && l == avoid {
						continue
					}
					if !fast && pass == 1 && (avoid == nil || l != avoid) {
						continue
					}
					if fast && classSlotsFull(l, true) {
						continue
					}
					if available, ok := l.mail.(interface{ DataAvailable() bool }); ok && !available.DataAvailable() {
						continue
					}
					// The reservation owns writer admission until it is consumed or
					// closed. Carrier shutdown must not unlock its journal in between.
					if !l.beginPhysicalWrite() {
						continue
					}
					// Durable admission may wait for fsync or another local writer.
					// Keep the journal alive without blocking group recovery/selection.
					g.mu.Unlock()
					grant, err := l.tryLocalGrant(bytes, fast)
					if err != nil {
						// A failed probe returns only its close/join guard, not a
						// physical slot or journal quota. Broadcasting here made
						// a full ledger wake its own waiters in a tight retry loop.
						l.releasePhysicalWriter(false)
					}
					g.mu.Lock()
					if err == nil && (g.lanes[keys[idx]] != l || !l.streamCarrierReady() || l.ctx.Err() != nil || ctx.Err() != nil) {
						g.mu.Unlock()
						closeErr := l.releaseReservedLocalGrant(grant)
						g.mu.Lock()
						if closeErr != nil {
							g.mu.Unlock()
							return nil, journal.Grant{}, closeErr
						}
						continue
					}
					if errors.Is(err, journal.ErrLimit) {
						continue
					}
					if err != nil {
						if l.ctx.Err() != nil {
							continue
						}
						g.mu.Unlock()
						return nil, journal.Grant{}, err
					}
					if fast {
						g.recordFastPlacementLocked(l.account.ID, time.Now())
					}
					g.turn++
					g.mu.Unlock()
					return l, grant, nil
				}
				if fast || avoid == nil {
					break
				}
			}
		}
		g.mu.Unlock()
		wake := capacityWake
		if fast {
			wake = capacityWake
		}
		var peerTimer *time.Timer
		var peerDeadline <-chan time.Time
		if probeBlocked && peerWait == 0 {
			// A concurrent cold-path admission probe may disappear without
			// freeing real capacity. Bound that advisory recheck; real slot,
			// receipt and quota transitions still wake immediately.
			peerWait = 100 * time.Millisecond
		}
		if peerWait > 0 {
			peerTimer = time.NewTimer(peerWait)
			peerDeadline = peerTimer.C
		}
		select {
		case <-ctx.Done():
			if peerTimer != nil {
				peerTimer.Stop()
			}
			return nil, journal.Grant{}, ctx.Err()
		case <-grace:
			fallback = true
		case <-wake:
		case <-peerDeadline:
		}
		if peerTimer != nil {
			peerTimer.Stop()
		}
	}
}

// Prefer less queued control/APPEND work for short DATA. Equal scores retain
// rotated order; bulk keeps its original round robin. The logical owner is a
// soft preference against sharing I/O, never a reason to pick a congested peer.
func (g *stripeGroup) prioritizeFastTargetsLocked(keys []string, avoid *lane) {
	if len(keys) == 0 {
		return
	}
	start := int(g.turn % uint64(len(keys)))
	rotated := append(append([]string(nil), keys[start:]...), keys[:start]...)
	copy(keys, rotated)
	quiet := g.interactiveReadersLocked()
	scores := make(map[string]int, len(keys))
	for _, key := range keys {
		l := g.lanes[key]
		if l == nil {
			scores[key] = int(^uint(0) >> 1)
			continue
		}
		n := int(l.predictedServiceMS(true)) + interactiveCarrierScore(l, quiet) + int(g.carrierLatencyLocked(l)) + g.fastPlacementPenaltyLocked(l.account.ID, time.Now())
		if pending := g.delivery.fastCarrierRanges[l.account.ID]; pending > 0 {
			// Tagged APPEND can finish long before the peer's short BODY/ACK.
			// Include client wallet publications as well as server journal debt.
			// A busy reserve must not keep winning every cold connection's turn.
			n += pending * 128
			if l == quiet[0] || l == quiet[1] {
				n += 512
			}
		}
		if l == avoid {
			n += 24
		}
		scores[key] = n
	}
	sort.SliceStable(keys, func(i, j int) bool { return scores[keys[i]] < scores[keys[j]] })
}

// An unused local grant must be durably closed before shutdown can close the
// journal. This also releases the writer admission acquired by selection.
func (l *lane) releaseReservedLocalGrant(grant journal.Grant) error {
	err := l.data.CloseGrant(grant.Ref)
	l.endPhysicalWrite()
	return err
}

// Keep the established fast + two bulk inbound leases even with legacy 8 MiB
// journals; divide additional byte capacity evenly between duplex directions.
func duplexDataShares(profileBytes uint64) (local, remote uint64) {
	remote = max(profileBytes/2, fastLeaseBytes+remoteBulkLeases*dataLeaseBytes)
	// A fenced grant remains durable debt while its replacement is published.
	// Reserve one bounded replacement without changing total journal capacity.
	if profileBytes >= 12<<20 {
		remote = max(remote, fastLeaseBytes+(remoteBulkLeases+1)*dataLeaseBytes)
	}
	return profileBytes - remote, remote
}

// Include active APPENDs before their outgoing BODY is registered. Only a quiet
// reader with real control/physical headroom receives the reserve preference.
// An in-flight control APPEND on a separate socket must not send short DATA
// into a bulk BODY queue; its bounded descriptor wait is scored within reserve.
// Keep that preference while any effective DATA slot is free, even when another
// short APPEND already occupies this reader's first slot.
func interactiveCarrierScore(l *lane, quiet [2]*lane) int {
	n := int(l.controlStallPenalty(time.Now())) + len(l.urgentQueue)*80 + len(l.controlQueue)*15 + len(l.maintenanceQueue)*5 + int(l.controlDebt.Load())*4
	if l.dataShards == 0 {
		// Mixed mail shares its BODY reader with short traffic. Split DATA
		// has its own mailbox/receiver and physical slot; its unread bulk
		// must not push a short reply away from the request's primed reader.
		n += int(l.unreceivedLocalRecords.Load())*16 + len(l.physicalSlots)*128
	}
	if l.controlWriting.Load() > 0 {
		n += 256
	}
	if (l == quiet[0] || l == quiet[1]) && l.unreceivedLocalRecords.Load() == 0 && l.unreceivedLocalBytes.Load() == 0 && l.controlDebt.Load() < 80 && !l.cleanupBlocksFast() && (l.independentDataMaintenance() || l.controlWriting.Load() == 0) && len(l.physicalSlots) < physicalSlotLimit(l) {
		n -= 512
	}
	return n
}
