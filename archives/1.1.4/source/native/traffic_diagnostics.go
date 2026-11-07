package mobile

import "sync/atomic"

type trafficDiagnostics struct {
	dnsWorkerRefusals, tcpWorkerRefusals                      atomic.Uint64
	openAttempts, openEstablished, openFailures, openTimeouts atomic.Uint64
	workerRefusals, ipv6Blocked, udpBlocked, dnsFailures      atomic.Uint64
	opening                                                   atomic.Int64
}

type trafficSnapshot struct {
	DNSWorkerRefusals, TCPWorkerRefusals                          uint64
	DNSWorkers, TCPWorkers                                        int
	OpenAttempts, OpenEstablished, OpenFailures, OpenTimeouts     uint64
	WorkerRefusals, IPv6Blocked, UDPBlocked, DNSFailures          uint64
	Opening, ActiveTCP                                            int64
	Upload, Download                                              uint64
	DNSIMAPFallback                                               bool
	DNSCacheHits, DNSCacheMisses, DNSSharedQueries                uint64
	DNSAddressCacheEntries, DNSReplyCacheEntries                  int
	TCPRetransmits, TCPTimeouts, TCPSendErrors, TUNDroppedPackets uint64
}

// Only cumulative counts and the session's resolver latch. No host names,
// resolver answers, socket addresses, query bytes or credential strings.
func (c *Client) trafficSnapshot() trafficSnapshot {
	d := &c.traffic
	s := trafficSnapshot{DNSWorkerRefusals: d.dnsWorkerRefusals.Load(), TCPWorkerRefusals: d.tcpWorkerRefusals.Load(), DNSWorkers: len(c.dnsSlots), TCPWorkers: len(c.slots), OpenAttempts: d.openAttempts.Load(), OpenEstablished: d.openEstablished.Load(), OpenFailures: d.openFailures.Load(), OpenTimeouts: d.openTimeouts.Load(), WorkerRefusals: d.workerRefusals.Load(), IPv6Blocked: d.ipv6Blocked.Load(), UDPBlocked: d.udpBlocked.Load(), DNSFailures: d.dnsFailures.Load(), Opening: d.opening.Load(), ActiveTCP: c.tcp.Load(), Upload: c.up.Load(), Download: c.down.Load()}
	// Read existing atomic TUN-stack counters only when a snapshot is requested.
	// No packet tracing, profiling worker or sampling timer runs on the phone.
	c.mu.Lock()
	nativeStack := c.stack
	c.mu.Unlock()
	if nativeStack != nil {
		stats := nativeStack.Stats()
		s.TCPRetransmits = stats.TCP.Retransmits.Value()
		s.TCPTimeouts = stats.TCP.Timeouts.Value()
		s.TCPSendErrors = stats.TCP.SegmentSendErrors.Value()
		s.TUNDroppedPackets = stats.DroppedPackets.Value()
	}
	if c.resolver != nil {
		s.DNSIMAPFallback = c.resolver.imap.Load()
		s.DNSCacheHits, s.DNSCacheMisses, s.DNSSharedQueries = c.resolver.cacheHits.Load(), c.resolver.cacheMisses.Load(), c.resolver.sharedQueries.Load()
		c.resolver.mu.Lock()
		s.DNSAddressCacheEntries, s.DNSReplyCacheEntries = len(c.resolver.cache), len(c.resolver.answers.entries)
		c.resolver.mu.Unlock()
	}
	return s
}
