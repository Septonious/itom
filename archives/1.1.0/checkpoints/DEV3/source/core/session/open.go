package session

import (
	"errors"
	"time"

	"itom.local/core/wire"
)

type DialToken struct{ Generation, Stream uint64 }
type DialJob struct {
	Token  DialToken
	Target wire.Connect
}
type OpenAction struct {
	Dial   *DialJob
	Reply  *wire.Frame
	Cancel *DialToken
	// A worker retains its socket until completion is accepted. If Adopt is
	// false it MUST close any returned socket (including on a stale event).
	Adopt bool
}

type opening struct {
	target              wire.Connect
	deadline, nextReply time.Duration
	pending             bool
	code                wire.Reason
}

// OpenBook is the server's bounded, actor-owned OPEN/dial state machine.
// No function here dials, resolves, blocks, or transfers a socket. Registry
// tombstones are never evicted within a session; exhaustion is explicit.
type OpenBook struct {
	generation uint64
	registry   *Registry
	entries    map[uint64]*opening
	policy     ControlPolicy
	lastNow    time.Duration
	stopped    bool
}

func NewOpenBook(generation uint64, maxActive, maxMetadata int, policy ControlPolicy) (*OpenBook, error) {
	if generation == 0 || !policy.valid() {
		return nil, ErrState
	}
	r, err := NewRegistry(maxActive, maxMetadata)
	if err != nil {
		return nil, err
	}
	return &OpenBook{generation: generation, registry: r, entries: map[uint64]*opening{}, policy: policy}, nil
}
func (b *OpenBook) check(generation uint64, now time.Duration) error {
	if generation != b.generation {
		return ErrGeneration
	}
	if b.stopped || now < 0 || now < b.lastNow {
		return ErrState
	}
	b.lastNow = now
	return nil
}
func (b *OpenBook) reply(id uint64, code wire.Reason) *wire.Frame {
	f, _ := wire.EncodeControl(id, wire.ConnectResult{Code: code})
	return &f
}
func (b *OpenBook) scheduleReply(e *opening, now time.Duration) {
	if now > time.Duration(1<<63-1)-b.policy.RetryInterval {
		e.nextReply = time.Duration(1<<63 - 1)
	} else {
		e.nextReply = now + b.policy.RetryInterval
	}
}
func (b *OpenBook) Receive(generation uint64, now time.Duration, f wire.Frame) (OpenAction, error) {
	if err := b.check(generation, now); err != nil {
		return OpenAction{}, err
	}
	m, err := wire.DecodeControl(f)
	if err != nil {
		return OpenAction{}, err
	}
	target, ok := m.(wire.Connect)
	if !ok {
		return OpenAction{}, ErrProtocol
	}
	if e := b.entries[f.Stream]; e != nil {
		if e.target != target {
			return OpenAction{}, ErrProtocol
		}
		if e.pending && now >= e.deadline {
			return b.expire(f.Stream, e, now), nil
		}
		if e.pending || now < e.nextReply {
			return OpenAction{}, nil
		}
		b.scheduleReply(e, now)
		return OpenAction{Reply: b.reply(f.Stream, e.code)}, nil
	}
	if b.registry.IsClosed(f.Stream) {
		return OpenAction{Reply: b.reply(f.Stream, wire.ReasonClosed)}, nil
	}
	if now > time.Duration(1<<63-1)-b.policy.Timeout {
		return OpenAction{}, ErrState
	}
	if _, err = b.registry.Open(f.Stream); err != nil {
		if errors.Is(err, ErrCapacity) && b.registry.Reject(f.Stream) == nil {
			// Socket pressure rejects this OPEN, not the whole owner. The
			// retained tombstone prevents a delayed duplicate from dialing.
			return OpenAction{Reply: b.reply(f.Stream, wire.ReasonCapacity)}, nil
		}
		return OpenAction{}, err
	}
	b.entries[f.Stream] = &opening{target: target, deadline: now + b.policy.Timeout, pending: true}
	job := DialJob{Token: DialToken{Generation: b.generation, Stream: f.Stream}, Target: target}
	return OpenAction{Dial: &job}, nil
}
func (b *OpenBook) expire(id uint64, e *opening, now time.Duration) OpenAction {
	e.pending, e.code = false, wire.ReasonTimeout
	b.scheduleReply(e, now)
	token := DialToken{Generation: b.generation, Stream: id}
	return OpenAction{Reply: b.reply(id, e.code), Cancel: &token}
}

// Complete returns Adopt=true exactly once on successful dial. The first
// terminal result wins; a duplicate, canceled or late success is never adopted.
func (b *OpenBook) Complete(token DialToken, now time.Duration, code wire.Reason) (OpenAction, error) {
	if err := b.check(token.Generation, now); err != nil {
		return OpenAction{}, err
	}
	if _, err := wire.EncodeControl(token.Stream, wire.ConnectResult{Code: code}); err != nil {
		return OpenAction{}, err
	}
	e := b.entries[token.Stream]
	if e == nil || !e.pending {
		return OpenAction{}, ErrState
	}
	if now >= e.deadline {
		return b.expire(token.Stream, e, now), nil
	}
	e.pending, e.code = false, code
	b.scheduleReply(e, now)
	return OpenAction{Reply: b.reply(token.Stream, code), Adopt: code == wire.ReasonNone}, nil
}

// PollOne keeps each call's output bounded; the owner schedules active IDs.
func (b *OpenBook) PollOne(generation, id uint64, now time.Duration) (OpenAction, error) {
	if err := b.check(generation, now); err != nil {
		return OpenAction{}, err
	}
	e := b.entries[id]
	if e == nil {
		return OpenAction{}, ErrID
	}
	if e.pending && now >= e.deadline {
		return b.expire(id, e, now), nil
	}
	return OpenAction{}, nil
}
func (b *OpenBook) DataAllowed(generation, id uint64) bool {
	e := b.entries[id]
	return !b.stopped && generation == b.generation && e != nil && !e.pending && e.code == wire.ReasonNone
}

// Retire follows completed terminal control/accounting. The owner closes its
// socket and cancels pending work. A compact tombstone prevents resurrection.
func (b *OpenBook) Retire(generation, id uint64) error {
	if b.stopped || generation != b.generation {
		return ErrGeneration
	}
	if err := b.registry.Close(id); err != nil {
		return err
	}
	delete(b.entries, id)
	return nil
}
func (b *OpenBook) Stop() {
	// Owner cancels its worker context and closes all adopted sockets.
	b.stopped = true
	clear(b.entries)
}
func (b *OpenBook) Counts() (active, tombstones int) { return b.registry.Counts() }
