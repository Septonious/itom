package session

import (
	"itom.local/core/wire"
	"time"
)

// Bootstrap runs AFTER Noise and the activation gate. Its input must be
// authenticated in that epoch. It does no disk I/O; the client must durably
// open its wallet with OpenBound before CommitStore acknowledges the offer.
type Bootstrap struct {
	role                      Role
	generation                uint64
	offer                     *wire.Coordinator
	pinned                    wire.ID
	policy                    ControlPolicy
	now, deadline, next       time.Duration
	sends                     int
	committed, ready, stopped bool
}
type BootstrapAction struct {
	Send     *wire.Frame
	Proposed *wire.Coordinator
	Ready    bool
}

func NewBootstrap(role Role, generation uint64, now time.Duration, store wire.ID, run uint64) (*Bootstrap, error) {
	p := DefaultControlPolicy()
	if role > Server || generation == 0 || now < 0 || now > time.Duration(1<<63-1)-p.Timeout {
		return nil, ErrState
	}
	b := &Bootstrap{role: role, generation: generation, pinned: store, policy: p, now: now, next: now, deadline: now + p.Timeout}
	if role == Server {
		m := wire.Coordinator{Store: store, Run: run, Features: 1}
		if _, e := wire.EncodeControl(0, m); e != nil {
			return nil, e
		}
		b.offer = &m
	} else if run != 0 {
		return nil, ErrState
	}
	return b, nil
}
func (b *Bootstrap) check(g uint64, now time.Duration) error {
	if g != b.generation {
		return ErrGeneration
	}
	if b.stopped || now < b.now {
		return ErrState
	}
	b.now = now
	if !b.ready && now >= b.deadline {
		b.stopped = true
		return ErrDeadline
	}
	return nil
}
func (b *Bootstrap) emit(now time.Duration) (BootstrapAction, error) {
	if b.offer == nil || now < b.next || now >= b.deadline || b.sends >= b.policy.MaxSends {
		return BootstrapAction{}, nil
	}
	m := *b.offer
	m.Ack = b.role == Client
	f, e := wire.EncodeControl(0, m)
	if e != nil {
		return BootstrapAction{}, e
	}
	b.sends++
	b.next = now + b.policy.RetryInterval
	return BootstrapAction{Send: &f, Ready: b.ready}, nil
}
func (b *Bootstrap) Poll(g uint64, now time.Duration) (BootstrapAction, error) {
	if e := b.check(g, now); e != nil {
		return BootstrapAction{}, e
	}
	if b.role == Server && !b.ready {
		return b.emit(now)
	}
	return BootstrapAction{Ready: b.ready}, nil
}
func (b *Bootstrap) Receive(g uint64, now time.Duration, f wire.Frame) (BootstrapAction, error) {
	if e := b.check(g, now); e != nil {
		return BootstrapAction{}, e
	}
	c, e := wire.DecodeControl(f)
	if e != nil {
		return BootstrapAction{}, e
	}
	m, ok := c.(wire.Coordinator)
	if !ok || m.Ack != (b.role == Server) {
		return BootstrapAction{}, ErrProtocol
	}
	if b.role == Server {
		expect := *b.offer
		expect.Ack = true
		if m != expect {
			return BootstrapAction{}, ErrProtocol
		}
		b.ready = true
		return BootstrapAction{Ready: true}, nil
	}
	if b.pinned != (wire.ID{}) && b.pinned != m.Store {
		return BootstrapAction{}, ErrProtocol
	}
	if b.offer != nil && *b.offer != m {
		return BootstrapAction{}, ErrProtocol
	}
	b.offer = &m
	if b.committed {
		return b.emit(now)
	}
	proposed := m
	return BootstrapAction{Proposed: &proposed}, nil
}
func (b *Bootstrap) CommitStore(g uint64, now time.Duration, store wire.ID) (BootstrapAction, error) {
	if e := b.check(g, now); e != nil {
		return BootstrapAction{}, e
	}
	if b.role != Client || b.offer == nil || b.offer.Store != store {
		return BootstrapAction{}, ErrState
	}
	if !b.committed {
		b.next = now
		b.sends = 0
	}
	b.committed = true
	b.ready = true
	return b.emit(now)
}
func (b *Bootstrap) Ready() bool { return b.ready && !b.stopped }
func (b *Bootstrap) Stop()       { b.stopped = true }
