package session

import (
	"errors"
	"time"

	"itom.local/core/wire"
)

var (
	ErrGeneration = errors.New("SESSION_GENERATION")
	ErrDeadline   = errors.New("SESSION_DEADLINE")
	ErrProtocol   = errors.New("SESSION_PROTOCOL")
)

type Role uint8

const (
	Client Role = iota
	Server
)

type GatePhase uint8

const (
	AwaitConfirm GatePhase = iota
	AwaitReady
	AwaitAck
	Established
	Terminated
)

type ControlPolicy struct {
	Timeout       time.Duration
	RetryInterval time.Duration
	MaxSends      int
}

func DefaultControlPolicy() ControlPolicy {
	return ControlPolicy{Timeout: 45 * time.Second, RetryInterval: 2 * time.Second, MaxSends: 16}
}
func (p ControlPolicy) valid() bool {
	return p.Timeout > 0 && p.Timeout <= 5*time.Minute && p.RetryInterval > 0 &&
		p.RetryInterval < p.Timeout && p.MaxSends >= 2 && p.MaxSends <= 64
}

// Gate runs after successful Noise split for an INITIAL session. It owns no
// goroutines, I/O, keys or clock. All calls belong to one actor; now is elapsed
// monotonic time, not wall-clock time. Rekey needs a separate epoch coordinator.
// Callers authenticate the record and check session/epoch binding before Receive.
type Gate struct {
	role                        Role
	generation                  uint64
	phase                       GatePhase
	policy                      ControlPolicy
	lastNow, deadline, nextSend time.Duration
	sends                       int
}

type GateAction struct {
	Send      []wire.Frame // At most one; seal with a NEW counter for each emission.
	Activated bool
	Deliver   bool // Authenticated application record may enter stream handling.
}

func NewGate(role Role, generation uint64, now time.Duration, policy ControlPolicy) (*Gate, GateAction, error) {
	if role > Server || generation == 0 || now < 0 || !policy.valid() ||
		now > time.Duration(1<<63-1)-policy.Timeout {
		return nil, GateAction{}, ErrState
	}
	g := &Gate{role: role, generation: generation, policy: policy, phase: AwaitConfirm,
		lastNow: now, deadline: now + policy.Timeout, nextSend: now}
	if role == Client {
		g.phase = AwaitReady
		return g, g.emit(wire.Confirm, now), nil
	}
	return g, GateAction{}, nil
}
func (g *Gate) Phase() GatePhase { return g.phase }
func (g *Gate) Ready() bool      { return g.phase == Established }
func (g *Gate) Stop()            { g.phase = Terminated }
func (g *Gate) check(generation uint64, now time.Duration) error {
	if generation != g.generation {
		return ErrGeneration
	}
	if g.phase == Terminated {
		return ErrState
	}
	if now < g.lastNow {
		return ErrState
	}
	g.lastNow = now
	if g.phase != Established && now >= g.deadline {
		g.Stop()
		return ErrDeadline
	}
	return nil
}
func (g *Gate) emit(kind wire.Type, now time.Duration) GateAction {
	if now >= g.deadline || now < g.nextSend || g.sends >= g.policy.MaxSends {
		return GateAction{}
	}
	g.sends++
	g.nextSend = g.deadline
	if g.policy.RetryInterval < g.deadline-now {
		g.nextSend = now + g.policy.RetryInterval
	}
	return GateAction{Send: []wire.Frame{{Type: kind}}}
}
func (g *Gate) Poll(generation uint64, now time.Duration) (GateAction, error) {
	if err := g.check(generation, now); err != nil {
		return GateAction{}, err
	}
	switch g.phase {
	case AwaitReady:
		return g.emit(wire.Confirm, now), nil
	case AwaitAck:
		return g.emit(wire.Ready, now), nil
	}
	return GateAction{}, nil
}
func (g *Gate) Receive(generation uint64, now time.Duration, f wire.Frame) (GateAction, error) {
	if err := g.check(generation, now); err != nil {
		return GateAction{}, err
	}
	if f.Type == wire.Data {
		if err := f.Validate(); err != nil {
			return GateAction{}, err
		}
	} else if _, err := wire.DecodeControl(f); err != nil {
		return GateAction{}, err
	}
	switch f.Type {
	case wire.Confirm:
		if g.role != Server {
			return GateAction{}, ErrProtocol
		}
		if g.phase == AwaitConfirm {
			g.phase = AwaitAck
		}
		return g.emit(wire.Ready, now), nil
	case wire.Ready:
		if g.role != Client {
			return GateAction{}, ErrProtocol
		}
		fresh := g.phase == AwaitReady
		if fresh {
			g.phase = Established
			// A new protocol step gets its own immediate response and retry
			// budget. Duplicate READY still obeys the cooldown below.
			g.nextSend, g.sends = now, 0
		}
		a := g.emit(wire.ReadyAck, now)
		a.Activated = fresh
		return a, nil
	case wire.ReadyAck:
		if g.role != Server || g.phase == AwaitConfirm {
			return GateAction{}, ErrProtocol
		}
		fresh := g.phase == AwaitAck
		g.phase = Established
		return GateAction{Activated: fresh}, nil // Never ACK an ACK.
	case wire.SessionClose, wire.SessionError:
		g.Stop()
		return GateAction{Deliver: true}, nil
	}
	if g.phase == AwaitAck && g.role == Server && f.Type == wire.Open {
		// An authenticated, structurally valid OPEN can only be emitted by a
		// ready client. It proves receipt of READY if READY_ACK was lost.
		g.phase = Established
		return GateAction{Activated: true, Deliver: true}, nil
	}
	if g.phase != Established {
		return GateAction{}, ErrState
	}
	return GateAction{Deliver: true}, nil
}
