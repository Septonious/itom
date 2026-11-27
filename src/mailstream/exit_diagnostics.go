package mailstream

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
)

// The server records the dial result before the existing RESET collapses
// remote failures into ErrClosed. Only counters leave the server; no wire or
// dialing policy change is needed to explain failed speedtest connections.
type exitOpenCounters struct {
	attempts, established, policy, dns, connect, timeout, canceled, other atomic.Uint64
}

type ExitOpenSnapshot struct {
	Attempts    uint64 `json:"attempts"`
	Established uint64 `json:"established"`
	Policy      uint64 `json:"policy"`
	DNS         uint64 `json:"dns"`
	Connect     uint64 `json:"connect"`
	Timeout     uint64 `json:"timeout"`
	Canceled    uint64 `json:"canceled"`
	Other       uint64 `json:"other"`
}

func (c *exitOpenCounters) record(err error, expired bool, ctxErr error) {
	var netErr net.Error
	switch {
	case expired, errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.Is(ctxErr, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		c.timeout.Add(1)
	case err == nil:
		c.established.Add(1)
	case errors.Is(ctxErr, context.Canceled), errors.Is(err, context.Canceled):
		c.canceled.Add(1)
	case errors.Is(err, ErrExitPolicy):
		c.policy.Add(1)
	case errors.Is(err, ErrExitDNS):
		c.dns.Add(1)
	case errors.Is(err, ErrExitConnect):
		c.connect.Add(1)
	default:
		c.other.Add(1)
	}
}

func (c *exitOpenCounters) snapshot() ExitOpenSnapshot {
	return ExitOpenSnapshot{c.attempts.Load(), c.established.Load(), c.policy.Load(), c.dns.Load(), c.connect.Load(), c.timeout.Load(), c.canceled.Load(), c.other.Load()}
}
