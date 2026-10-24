package transport

import "itom.local/core/wire"

// WindowScope is a logical byte-credit identity, not a physical quota key.
// These state machines are session-owned and must be called under that owner's
// lock, after authenticating control scope. Wire/session integration is pending.
type WindowScope struct {
	Session            wire.ID
	Generation, Stream uint64
	Direction          WindowDirection
}
type WindowDirection uint8

const (
	ClientToServer WindowDirection = iota + 1
	ServerToClient
)

func (s WindowScope) valid() bool {
	return s.Session != (wire.ID{}) && s.Generation != 0 && s.Stream > 0 && s.Stream <= maxStreamID && (s.Direction == ClientToServer || s.Direction == ServerToClient)
}

type WindowGrant struct {
	Scope        WindowScope
	Epoch, Limit uint64
}
type WindowRecall struct {
	Scope        WindowScope
	Epoch, Limit uint64
}

// WindowFence is the sender's authenticated promise that it stopped using old
// credit at Sent. It is never storage/deletion proof and cannot refund quota.
type WindowFence struct {
	Scope       WindowScope
	Epoch, Sent uint64
}

type ReceiveWindow struct {
	scope                                                         WindowScope
	epoch, consumed, observed, base, reserved, ceiling, highWater uint64
	pending                                                       *WindowRecall
	pendingBase, recallObserved                                   uint64
	lastFence                                                     WindowFence
}
type ReceiveWindowSnapshot struct {
	Epoch, Consumed, Observed, Base, Reserved, Limit uint64
	RecallPending                                    bool
}

func NewReceiveWindow(scope WindowScope, initial, ceiling uint64) (*ReceiveWindow, error) {
	if !scope.valid() || initial == 0 || initial > ceiling || ceiling > 1<<30 {
		return nil, ErrProtocol
	}
	return &ReceiveWindow{scope: scope, epoch: 1, base: initial, reserved: initial, ceiling: ceiling}, nil
}
func windowEdge(consumed, reserved uint64) uint64 {
	if reserved > maxStreamID-consumed {
		return maxStreamID
	}
	return consumed + reserved
}
func positiveDifference(a, b uint64) uint64 {
	if a <= b {
		return 0
	}
	return a - b
}
func (r *ReceiveWindow) Grant() WindowGrant {
	return WindowGrant{Scope: r.scope, Epoch: r.epoch, Limit: windowEdge(r.consumed, r.reserved)}
}
func (r *ReceiveWindow) Snapshot() ReceiveWindowSnapshot {
	g := r.Grant()
	return ReceiveWindowSnapshot{Epoch: r.epoch, Consumed: r.consumed, Observed: r.observed, Base: r.base, Reserved: r.reserved, Limit: g.Limit, RecallPending: r.pending != nil}
}

// Observe records the highest authenticated DATA end, including reordering.
// The session still owns byte-overlap/reassembly validation and actual payload.
func (r *ReceiveWindow) Observe(end uint64) error {
	if end > r.Grant().Limit {
		return ErrProtocol
	}
	r.observed = max(r.observed, end)
	return nil
}

// ConsumeTo advances only actual contiguous application consumption supplied by
// the owning session. Returned bytes are LOCAL MEMORY reservation released by
// an already-fenced shrink; they grant no mailbox/storage refund authority.
func (r *ReceiveWindow) ConsumeTo(consumed uint64) (uint64, error) {
	if consumed < r.consumed || consumed > r.observed {
		return 0, ErrProtocol
	}
	r.consumed = consumed
	if r.pending != nil {
		return 0, nil
	}
	needed := max(r.base, positiveDifference(r.highWater, consumed))
	if needed >= r.reserved {
		return 0, nil
	}
	freed := r.reserved - needed
	r.reserved = needed
	return freed, nil
}

// Grow requires actual free receiver memory. The owning session must account
// the returned additional reservation atomically BEFORE advertising Grant().
func (r *ReceiveWindow) Grow(target, available uint64) (uint64, error) {
	if target == 0 || target > r.ceiling {
		return 0, ErrCapacity
	}
	if r.pending != nil {
		return 0, ErrCapacity
	}
	if target <= r.base {
		return 0, nil
	}
	needed := max(target, positiveDifference(r.highWater, r.consumed))
	additional := positiveDifference(needed, r.reserved)
	if additional > available {
		return 0, ErrCapacity
	}
	r.base, r.reserved = target, needed
	return additional, nil
}

// BeginRecall retains ALL previous backing. Its provisional smaller grant lets
// an idle sender retain short-work capacity without waiting another round trip.
func (r *ReceiveWindow) BeginRecall(base uint64) (WindowRecall, error) {
	if r.pending != nil || base == 0 || base >= r.base || r.epoch == ^uint64(0) {
		return WindowRecall{}, ErrProtocol
	}
	request := WindowRecall{Scope: r.scope, Epoch: r.epoch + 1, Limit: windowEdge(r.consumed, base)}
	r.pending = &request
	r.pendingBase = base
	r.recallObserved = r.observed
	return request, nil
}

// PendingRecall returns the exact original request for bounded control recovery;
// recomputing its edge under the same epoch after consumption would be unsafe.
func (r *ReceiveWindow) PendingRecall() (WindowRecall, bool) {
	if r.pending == nil {
		return WindowRecall{}, false
	}
	return *r.pending, true
}

// Fence accepts a matching sender watermark before returning any old backing.
// Old bytes up to Sent can still arrive later; new bytes may overtake the fence
// only within the provisional grant. That combined exposure remains backed.
func (r *ReceiveWindow) Fence(f WindowFence) (uint64, error) {
	if f.Scope != r.scope || f.Epoch == 0 || f.Sent > maxStreamID {
		return 0, ErrProtocol
	}
	if f.Epoch < r.epoch {
		return 0, nil
	}
	if f.Epoch == r.epoch {
		if f == r.lastFence {
			return 0, nil
		}
		return 0, ErrProtocol
	}
	if r.pending == nil || f.Epoch != r.pending.Epoch || f.Sent < r.recallObserved || f.Sent > r.Grant().Limit || r.observed > max(f.Sent, r.pending.Limit) {
		return 0, ErrProtocol
	}
	needed := max(r.pendingBase, positiveDifference(f.Sent, r.consumed))
	if needed > r.reserved {
		return 0, ErrProtocol
	}
	freed := r.reserved - needed
	r.epoch, r.base, r.reserved, r.highWater = f.Epoch, r.pendingBase, needed, f.Sent
	r.pending = nil
	r.pendingBase = 0
	r.lastFence = f
	return freed, nil
}

type SendWindow struct {
	scope                       WindowScope
	epoch, limit, sent, ceiling uint64
	lastRecall                  WindowRecall
	lastFence                   WindowFence
}
type SendWindowSnapshot struct{ Epoch, Limit, Sent, Available uint64 }

func NewSendWindow(scope WindowScope, initial, ceiling uint64) (*SendWindow, error) {
	if !scope.valid() || initial == 0 || initial > ceiling || ceiling > 1<<30 {
		return nil, ErrProtocol
	}
	return &SendWindow{scope: scope, epoch: 1, limit: initial, ceiling: ceiling}, nil
}
func (s *SendWindow) Snapshot() SendWindowSnapshot {
	return SendWindowSnapshot{Epoch: s.epoch, Limit: s.limit, Sent: s.sent, Available: positiveDifference(s.limit, s.sent)}
}
func (s *SendWindow) SentTo(end uint64) error {
	if end < s.sent || end > s.limit {
		return ErrProtocol
	}
	s.sent = end
	return nil
}
func (s *SendWindow) ApplyGrant(g WindowGrant) error {
	if g.Scope != s.scope || g.Epoch == 0 || g.Limit > maxStreamID {
		return ErrProtocol
	}
	if g.Epoch < s.epoch {
		return nil
	} // delayed old credit must NEVER reopen a returned window
	if g.Epoch != s.epoch {
		return ErrProtocol
	}
	if g.Limit <= s.limit {
		return nil
	}
	if g.Limit < s.sent || g.Limit-s.sent > s.ceiling {
		return ErrProtocol
	}
	s.limit = g.Limit
	return nil
}

// Recall switches the sender's credit epoch and fences old dispatch immediately.
// A duplicate returns the ORIGINAL watermark even if new-epoch DATA has since
// been sent; it cannot enlarge the old exposure during delayed-control recovery.
func (s *SendWindow) Recall(r WindowRecall) (WindowFence, bool, error) {
	if r.Scope != s.scope || r.Epoch == 0 || r.Limit > maxStreamID {
		return WindowFence{}, false, ErrProtocol
	}
	if r.Epoch < s.epoch {
		return WindowFence{}, false, nil
	}
	if r.Epoch == s.epoch {
		if r == s.lastRecall {
			return s.lastFence, true, nil
		}
		return WindowFence{}, false, ErrProtocol
	}
	if s.epoch == ^uint64(0) || r.Epoch != s.epoch+1 || positiveDifference(r.Limit, s.sent) > s.ceiling {
		return WindowFence{}, false, ErrProtocol
	}
	f := WindowFence{Scope: s.scope, Epoch: r.Epoch, Sent: s.sent}
	s.epoch = r.Epoch
	s.limit = max(s.sent, r.Limit)
	s.lastRecall = r
	s.lastFence = f
	return f, true, nil
}
