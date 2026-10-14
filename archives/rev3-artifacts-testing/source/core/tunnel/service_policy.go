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
	if l.serviceSamples < 4 || l.serviceNS <= uint64(400*time.Millisecond) {
		return datapath.BulkBatchTargetBytes
	}
	// A slow literal must not occupy its service for seconds. Base64/envelope
	// overhead is conservative; complete 32 KiB records remain indivisible.
	bytes := l.serviceBytes * uint64(250*time.Millisecond) / l.serviceNS * 3 / 4
	return min(uint64(datapath.BulkBatchTargetBytes), max(uint64(32<<10), bytes))
}
func (l *lane) BulkBatchPolicy() (uint64, time.Duration) {
	if l.stripe == nil {
		return l.serviceBatchTarget(), 20 * time.Millisecond
	}
	g := l.stripe
	g.mu.Lock()
	defer g.mu.Unlock()
	var fits []uint64
	for _, carrier := range g.lanes {
		if carrier != nil && carrier.streamCarrierReady() && carrier.ctx.Err() == nil {
			fits = append(fits, carrier.serviceBatchTarget())
		}
	}
	if len(fits) == 0 {
		return datapath.BulkBatchTargetBytes, 20 * time.Millisecond
	}
	// One bad account cannot shrink every healthy account's batch.
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
