// Package recovery makes timeout decisions on caller-supplied monotonic time.
// Poll never authorizes a DATA retry. Only an authenticated NACK does so.
package recovery

import (
	"errors"
	"time"
)

var ErrState = errors.New("RECOVERY_STATE")

type Action uint8

const (
	Wait Action = iota
	ProbeStatus
	DeadlineExpired
)

type Watch struct {
	deadline, nextProbe time.Duration
	interval            time.Duration
	probes, maxProbes   int
	lastNow             time.Duration
	done                bool
}

func New(now, deadline, interval time.Duration, maxProbes int) (*Watch, error) {
	if now < 0 || deadline <= now || interval <= 0 || maxProbes < 1 || maxProbes > 64 {
		return nil, ErrState
	}
	return &Watch{deadline: deadline, nextProbe: now, interval: interval, maxProbes: maxProbes, lastNow: now}, nil
}
func (w *Watch) Receipt() { w.done = true }
func (w *Watch) Poll(now time.Duration) (Action, error) {
	if now < w.lastNow {
		return Wait, ErrState
	}
	w.lastNow = now
	if w.done {
		return Wait, nil
	}
	if now >= w.deadline {
		w.done = true
		return DeadlineExpired, nil
	}
	if now >= w.nextProbe && w.probes < w.maxProbes {
		w.probes++
		if w.interval > w.deadline-now {
			w.nextProbe = w.deadline
		} else {
			w.nextProbe = now + w.interval
		}
		return ProbeStatus, nil
	}
	return Wait, nil
}

// Repair permits at most two attempts after the original APPEND. NACK IDs are
// strictly increasing per item. A duplicate NACK cannot amplify traffic.
type Repair struct {
	attempts int
	lastNack uint64
	inFlight bool
}

func NewRepair() *Repair { return &Repair{attempts: 1} }
func (r *Repair) Nack(id uint64) bool {
	if id == 0 || id <= r.lastNack {
		return false
	}
	r.lastNack = id
	if r.inFlight || r.attempts >= 3 {
		return false
	}
	r.attempts++
	r.inFlight = true
	return true
}
func (r *Repair) Complete()     { r.inFlight = false }
func (r *Repair) Attempts() int { return r.attempts }
