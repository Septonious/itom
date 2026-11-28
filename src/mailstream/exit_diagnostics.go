package mailstream

import (
	"sync/atomic"
)

// Count dial results before transmitting the authenticated fixed RESET reason.
type exitOpenCounters struct {
	attempts, established, policy, dns, connect, timeout, canceled, other atomic.Uint64
	relayDial, relayTLS, relaySetup, relayCooldown                        atomic.Uint64
}

type ExitOpenSnapshot struct {
	Attempts      uint64 `json:"attempts"`
	Established   uint64 `json:"established"`
	Policy        uint64 `json:"policy"`
	DNS           uint64 `json:"dns"`
	Connect       uint64 `json:"connect"`
	Timeout       uint64 `json:"timeout"`
	Canceled      uint64 `json:"canceled"`
	Other         uint64 `json:"other"`
	RelayDial     uint64 `json:"relay_dial"`
	RelayTLS      uint64 `json:"relay_tls"`
	RelaySetup    uint64 `json:"relay_setup"`
	RelayCooldown uint64 `json:"relay_cooldown"`
}

func (c *exitOpenCounters) record(err error, expired bool, ctxErr error) {
	if err == nil && !expired && ctxErr == nil {
		c.established.Add(1)
		return
	}
	switch ClassifyOpenFailure(err, expired, ctxErr) {
	case OpenFailurePolicy:
		c.policy.Add(1)
	case OpenFailureDNS:
		c.dns.Add(1)
	case OpenFailureConnect:
		c.connect.Add(1)
	case OpenFailureTimeout:
		c.timeout.Add(1)
	case OpenFailureCanceled:
		c.canceled.Add(1)
	case OpenFailureRelayDial:
		c.relayDial.Add(1)
	case OpenFailureRelayTLS:
		c.relayTLS.Add(1)
	case OpenFailureRelaySetup:
		c.relaySetup.Add(1)
	case OpenFailureRelayCooldown:
		c.relayCooldown.Add(1)
	default:
		c.other.Add(1)
	}
}

func (c *exitOpenCounters) snapshot() ExitOpenSnapshot {
	return ExitOpenSnapshot{c.attempts.Load(), c.established.Load(), c.policy.Load(), c.dns.Load(), c.connect.Load(), c.timeout.Load(), c.canceled.Load(), c.other.Load(), c.relayDial.Load(), c.relayTLS.Load(), c.relaySetup.Load(), c.relayCooldown.Load()}
}
