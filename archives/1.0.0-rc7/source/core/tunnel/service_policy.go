package tunnel

import (
	"itom.local/core/datapath"
	"time"
)

// Per-direction physical service observations are kept apart from logical ACK
// and application RTT. They influence placement/batch size, never quota proof.
func (l *lane) observeDataService(bytes int, d time.Duration, ok bool) {
	l.observeDataServiceClass(bytes, d, ok, false)
}

func (l *lane) observeDataServiceClass(bytes int, d time.Duration, ok, fast bool) {
	if !ok || bytes <= 0 || d <= 0 {
		return
	}
	l.serviceMu.Lock()
	defer l.serviceMu.Unlock()
	ns := uint64(d)
	n := uint64(bytes)
	serviceNS, serviceBytes, samples := &l.serviceNS, &l.serviceBytes, &l.serviceSamples
	if fast {
		serviceNS, serviceBytes, samples = &l.fastServiceNS, &l.fastServiceBytes, &l.fastServiceSamples
	}
	if *samples == 0 {
		*serviceNS = ns
		*serviceBytes = n
	} else {
		*serviceNS = (7**serviceNS + ns) / 8
		*serviceBytes = (7**serviceBytes + n) / 8
	}
	*samples++
}
func (l *lane) serviceBatchTarget() uint64 {
	l.serviceMu.Lock()
	defer l.serviceMu.Unlock()
	target := uint64(datapath.BulkBatchTargetBytes)
	if l.serviceSamples >= 4 && l.serviceNS > uint64(400*time.Millisecond) {
		// A slow literal must not occupy its service for seconds. Base64/envelope
		// overhead is conservative; complete 32 KiB records remain indivisible.
		bytes := l.serviceBytes * uint64(250*time.Millisecond) / l.serviceNS * 3 / 4
		target = min(target, max(uint64(32<<10), bytes))
	}
	if l.side && (l.peerReadAt.IsZero() || time.Since(l.peerReadAt) > 30*time.Second) {
		// A recently and repeatedly proven peer BODY service can resume one
		// conservative batch after a quiet spell. A first connection, repaired
		// slow path or stale history still starts with a single 32 KiB record.
		// This never creates grant authority or substitutes APPEND for a receipt.
		resume := uint64(32 << 10)
		if !l.peerReadAt.IsZero() && time.Since(l.peerReadAt) <= 90*time.Second &&
			l.peerReadSamples >= 8 && l.peerReadSlowCount == 0 &&
			l.peerReadBatchTarget == 0 {
			resume = 128 << 10
		}
		target = min(target, resume)
	} else if l.peerReadBatchTarget != 0 && time.Since(l.peerReadAt) <= 30*time.Second {
		target = min(target, l.peerReadBatchTarget)
	}
	return target
}

// Complete authenticated MAIL_RECEIPTs describe the peer BODY service, unlike
// tagged APPEND. This is advisory batching only: it changes neither RTT, the
// flight/window authority, nor durable quota. Known-successful physical bulk
// copies alone train it; a repair has distinct authenticated counters, so its
// physical completion is unambiguous too. Two slow samples reject an isolated visibility tail.
// A recovered path doubles the target after two short successful turns.
func (l *lane) observePeerReadService(bytes int64, started time.Time, now time.Time) {
	if bytes < 32<<10 || bytes > dataBatchMIMEHardCap || started.IsZero() || !now.After(started) {
		return
	}
	// The server has no client-side NOOP baseline. Use the authenticated
	// unloaded control echo as propagation evidence instead of treating every
	// mobile/split-mailbox receipt over one second as slow BODY service. Loaded
	// RTT cannot raise this floor; APPEND and short DATA still cannot train bulk.
	budget := max(time.Second, 4*l.providerBaseline, 4*time.Duration(l.idleRTT.Load())*time.Millisecond)
	elapsed := now.Sub(started)
	l.serviceMu.Lock()
	defer l.serviceMu.Unlock()
	if now.Sub(l.peerReadAt) > 30*time.Second {
		l.peerReadBatchTarget, l.peerReadSlowCount, l.peerReadFastCount = 32<<10, 0, 0
	}
	l.peerReadAt = now
	l.peerReadSamples++
	// RC5 cold-ramp: one complete authenticated, promptly read physical BODY
	// proves the initial 32 KiB path is usable. Permit just one tentative
	// 64 KiB next batch instead of waiting for a second full mail round trip.
	// Subsequent increases still require the original two fast receipts.
	// This never observes APPEND-only success or grants extra physical quota.
	if l.peerReadSamples == 1 && l.peerReadBatchTarget == 32<<10 && elapsed <= budget {
		l.peerReadBatchTarget = 64 << 10
		l.peerReadFastCount = 0
		return
	}
	if elapsed > 2*budget {
		l.peerReadFastCount = 0
		l.peerReadSlowCount = min(l.peerReadSlowCount+1, 16)
		if l.peerReadSlowCount >= 2 {
			target := min(uint64(datapath.BulkBatchTargetBytes), max(uint64(32<<10), uint64(bytes)*uint64(budget)/uint64(elapsed)))
			if l.peerReadBatchTarget == 0 || target < l.peerReadBatchTarget {
				l.peerReadBatchTarget = target
			}
		}
		return
	}
	l.peerReadSlowCount = 0
	if elapsed <= budget && l.peerReadBatchTarget != 0 {
		l.peerReadFastCount++
		if l.peerReadFastCount >= 2 {
			l.peerReadFastCount = 0
			l.peerReadBatchTarget = min(uint64(datapath.BulkBatchTargetBytes), 2*l.peerReadBatchTarget)
			if l.peerReadBatchTarget == uint64(datapath.BulkBatchTargetBytes) {
				l.peerReadBatchTarget = 0
			}
		}
	} else {
		l.peerReadFastCount = 0
	}
}
func (l *lane) BulkBatchPolicy() (uint64, time.Duration) {
	if l.stripe == nil {
		return l.serviceBatchTarget(), 20 * time.Millisecond
	}
	g := l.stripe
	g.mu.Lock()
	defer g.mu.Unlock()
	var fits []uint64
	quiet := g.interactiveReadersLocked()
	for _, carrier := range g.lanes {
		if carrier != nil && carrier.streamCarrierReady() && carrier.ctx.Err() == nil {
			if carrier == quiet[0] || carrier == quiet[1] {
				continue
			}
			fits = append(fits, carrier.serviceBatchTarget())
		}
	}
	if len(fits) == 0 {
		return datapath.BulkBatchTargetBytes, 20 * time.Millisecond
	}
	// One bad bulk account cannot shrink healthy bulk accounts. Quiet reserves
	// do not carry bulk and cannot testify to its downstream service rate.
	for i := 1; i < len(fits); i++ {
		for j := i; j > 0 && fits[j] < fits[j-1]; j-- {
			fits[j], fits[j-1] = fits[j-1], fits[j]
		}
	}
	return fits[len(fits)/2], 20 * time.Millisecond
}
func (l *lane) predictedServiceMS(fast bool) int64 {
	l.serviceMu.Lock()
	ns, bytes, samples := l.serviceNS, l.serviceBytes, l.serviceSamples
	if fast {
		ns, bytes, samples = l.fastServiceNS, l.fastServiceBytes, l.fastServiceSamples
	}
	l.serviceMu.Unlock()
	if fast {
		service := int64(150)
		if samples >= 4 {
			service = min(int64(1000), max(int64(1), time.Duration(ns).Milliseconds()))
		}
		return int64(len(l.fastPhysicalSlots))*service + int64(len(l.urgentQueue))*10
	}
	if samples < 4 || bytes == 0 {
		// Cold accounts have no trustworthy rate yet, but pending admission
		// is still real work. Do not put the first fsync wave on one carrier
		// just because its APPEND socket has not been claimed yet.
		active := max(int64(len(l.physicalSlots)), l.physicalActive.Load())
		return min(int64(5000), max(int64(0), active)*150)
	}
	pending := uint64(max(int64(0), l.unreceivedLocalBytes.Load()))
	service := time.Duration(ns)
	// Include durable admission already in progress, before APPEND has claimed
	// its socket. Otherwise concurrent collectors all select the same free-looking
	// account while its first grant fsync is still pending.
	active := max(int64(len(l.physicalSlots)), l.physicalActive.Load())
	return min(int64(5000), service.Milliseconds()*(1+active)+int64(pending/bytes)*service.Milliseconds())
}

// A small envelope alone cannot bound a paced reader if fast APPENDs can queue
// many envelopes behind it. Keep one known-successful physical bulk BODY in
// flight before the first observation or after sustained pacing; two during
// measured convergence avoid serializing every mobile receipt handshake. Unknown outcomes retain
// their journal debt but cannot hold this advisory scheduling gate forever.
func (l *lane) peerReadWindowFull() bool {
	full, _ := l.peerReadWindowWait()
	return full
}

func (l *lane) peerReadWindowWait() (bool, time.Duration) {
	if !l.side {
		return false, 0
	}
	l.serviceMu.Lock()
	constrained := l.peerReadAt.IsZero() || time.Since(l.peerReadAt) > 30*time.Second || l.peerReadBatchTarget != 0
	limit := int64(1)
	if !l.peerReadAt.IsZero() && l.peerReadSlowCount < 2 {
		limit = 2
	}
	l.serviceMu.Unlock()
	pending := l.peerReadPendingBodies.Load()
	// Receipt waits are advisory, not a second durable reservation. Across a
	// visibility gap or carrier loss an old physical copy may never produce a
	// current receipt. Let a fresh bounded publication probe delivery after the
	// lease; keep every old counter, physical charge and cleanup proof intact.
	lease := max(3*time.Second, 4*time.Duration(l.idleRTT.Load())*time.Millisecond)
	remaining := time.Duration(0)
	if stamp := l.peerReadNewestPendingAt.Load(); stamp > 0 {
		remaining = lease - time.Since(time.Unix(0, stamp))
	}
	if remaining <= 0 {
		pending = 0
	}
	full := constrained && l.physicalActive.Load()+pending >= limit
	if !full || pending == 0 {
		return full, 0
	}
	return full, remaining
}
