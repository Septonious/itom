package mobile

import "sync/atomic"

type trafficDiagnostics struct {
	openAttempts, openEstablished, openFailures, openTimeouts atomic.Uint64
	workerRefusals, ipv6Blocked, udpBlocked, dnsFailures      atomic.Uint64
	opening                                                   atomic.Int64
}

type trafficSnapshot struct {
	OpenAttempts, OpenEstablished, OpenFailures, OpenTimeouts uint64
	WorkerRefusals, IPv6Blocked, UDPBlocked, DNSFailures      uint64
	Opening, ActiveTCP                                        int64
	Upload, Download                                          uint64
	DNSIMAPFallback                                           bool
}

// Only cumulative counts and the session's resolver latch. No host names,
// resolver answers, socket addresses, query bytes or credential strings.
func (c *Client) trafficSnapshot() trafficSnapshot {
	d := &c.traffic
	s := trafficSnapshot{OpenAttempts: d.openAttempts.Load(), OpenEstablished: d.openEstablished.Load(), OpenFailures: d.openFailures.Load(), OpenTimeouts: d.openTimeouts.Load(), WorkerRefusals: d.workerRefusals.Load(), IPv6Blocked: d.ipv6Blocked.Load(), UDPBlocked: d.udpBlocked.Load(), DNSFailures: d.dnsFailures.Load(), Opening: d.opening.Load(), ActiveTCP: c.tcp.Load(), Upload: c.up.Load(), Download: c.down.Load()}
	if c.resolver != nil {
		s.DNSIMAPFallback = c.resolver.imap.Load()
	}
	return s
}
