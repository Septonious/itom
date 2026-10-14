// Package flow owns byte accounting. Methods are synchronous: a session actor
// serializes calls and authenticates all peer-originated events first.
package flow

import (
	"errors"
	"fmt"
	"sort"

	"itom.local/core/wire"
)

var (
	ErrCredit   = errors.New("CREDIT_EXHAUSTED")
	ErrState    = errors.New("FLOW_STATE")
	ErrReceipt  = errors.New("INVALID_RECEIPT")
	ErrUnknown  = errors.New("UNKNOWN_ITEM")
	ErrConflict = errors.New("CONFLICTING_OUTCOME")
)

type Stage uint8

const (
	Queued Stage = iota
	Reserved
	Writing
	VisibleWait
)

type Outcome uint8

const (
	Pending Outcome = iota
	Committed
	NotCommitted
	CommitUnknown
)

type Limits struct {
	Logical  uint64
	Items    int
	Attempts int
	Streams  int
}

func DefaultLimits() Limits { return Limits{12 << 20, 4096, 8192, 128} }

type item struct {
	id, stream, offset uint64
	payload            []byte
	stage              Stage
	account            string
	started            int
	provenCommitted    bool
}
type Attempt struct {
	ID, Item, Stream, Offset, PayloadBytes, EncodedBytes uint64
	Account                                              string
	Outcome                                              Outcome
	Cleaned                                              bool
	Repair                                               bool
}
type txStream struct{ next, receipt, peerMax, attempted uint64 }
type Tx struct {
	limits                    Limits
	caps, debt                map[string]uint64
	items                     map[uint64]*item
	attempts                  map[uint64]*Attempt
	streams                   map[uint64]*txStream
	nextItem, nextAttempt     uint64
	logical                   uint64
	peerMaxData, peerConsumed uint64
}
type TxSnapshot struct {
	Logical          uint64
	Stages           [4]uint64
	AccountDebt      map[string]uint64
	Items, Attempts  int
	PhysicalExposure uint64
}

func NewTx(l Limits, caps map[string]uint64) (*Tx, error) {
	if l.Logical == 0 || l.Logical > 1<<30 || l.Items < 1 || l.Items > 65536 || l.Attempts < 1 ||
		l.Attempts > 65536 || l.Streams < 1 || l.Streams > 4096 || len(caps) == 0 || len(caps) > 64 {
		return nil, ErrCredit
	}
	t := &Tx{limits: l, caps: map[string]uint64{}, debt: map[string]uint64{},
		items: map[uint64]*item{}, attempts: map[uint64]*Attempt{}, streams: map[uint64]*txStream{},
		nextItem: 1, nextAttempt: 1}
	for id, n := range caps {
		if id == "" || len(id) > 64 || n == 0 || n > l.Logical {
			return nil, ErrCredit
		}
		t.caps[id] = n
	}
	return t, nil
}
func (t *Tx) OpenStream(id uint64) error {
	if id == 0 || id%2 != 1 || id > wire.MaxOffset {
		return ErrState
	}
	if _, ok := t.streams[id]; ok {
		return ErrState
	}
	if len(t.streams) >= t.limits.Streams {
		return ErrCredit
	}
	t.streams[id] = &txStream{}
	return nil
}

// Authenticated absolute WINDOW updates are monotonic. Until both limits are
// advertised, a new sender has zero credit. Retries never consume it twice.
func (t *Tx) ConnectionLimit(maximum uint64) error {
	if maximum < t.peerMaxData || maximum > wire.MaxOffset {
		return ErrCredit
	}
	t.peerMaxData = maximum
	return nil
}
func (t *Tx) StreamLimit(id, maximum uint64) error {
	s := t.streams[id]
	if s == nil {
		return ErrUnknown
	}
	if maximum < s.peerMax || maximum > wire.MaxOffset {
		return ErrCredit
	}
	s.peerMax = maximum
	return nil
}

// The outer stream registry prevents reuse after ForgetStream.
func (t *Tx) Admit(stream uint64, data []byte) (uint64, error) {
	s := t.streams[stream]
	if s == nil {
		return 0, ErrUnknown
	}
	n := uint64(len(data))
	if n == 0 || n > wire.MaxData || n > t.limits.Logical-t.logical ||
		len(t.items) >= t.limits.Items || s.next > wire.MaxOffset-n || t.nextItem == 0 ||
		n > t.peerMaxData-t.peerConsumed || s.next+n > s.peerMax {
		return 0, ErrCredit
	}
	id := t.nextItem
	t.nextItem++
	t.items[id] = &item{id: id, stream: stream, offset: s.next, payload: append([]byte(nil), data...)}
	s.next += n
	t.logical += n
	t.peerConsumed += n
	return id, nil
}
func (t *Tx) Reserve(id uint64, account string) error {
	it := t.items[id]
	if it == nil {
		return ErrUnknown
	}
	if it.stage != Queued {
		return ErrState
	}
	cap, ok := t.caps[account]
	n := uint64(len(it.payload))
	if !ok || t.debt[account] > cap || n > cap-t.debt[account] {
		return ErrCredit
	}
	t.debt[account] += n
	it.account = account
	it.stage = Reserved
	return nil
}
func (t *Tx) CancelReservation(id uint64) error {
	it := t.items[id]
	if it == nil {
		return ErrUnknown
	}
	if it.stage != Reserved {
		return ErrState
	}
	t.debt[it.account] -= uint64(len(it.payload))
	it.account = ""
	it.stage = Queued
	return nil
}
func (t *Tx) begin(it *item, account string, encoded uint64, repair bool) (Attempt, error) {
	if encoded == 0 || encoded > 2<<20 || len(t.attempts) >= t.limits.Attempts || it.started >= 3 ||
		t.nextAttempt == 0 {
		return Attempt{}, ErrCredit
	}
	if _, ok := t.caps[account]; !ok {
		return Attempt{}, ErrUnknown
	}
	if repair {
		for _, a := range t.attempts {
			if a.Item == it.id && a.Repair && a.Outcome == Pending {
				return Attempt{}, ErrState
			}
		}
	}
	a := Attempt{ID: t.nextAttempt, Item: it.id, Stream: it.stream, Offset: it.offset,
		PayloadBytes: uint64(len(it.payload)), EncodedBytes: encoded, Account: account, Repair: repair}
	t.nextAttempt++
	it.started++
	t.attempts[a.ID] = &a
	stream := t.streams[it.stream]
	if end := it.offset + uint64(len(it.payload)); end > stream.attempted {
		stream.attempted = end
	}
	if !repair {
		it.stage = Writing
	}
	return a, nil
}

// A durable intent and a physical grant must exist before the worker performs I/O.
// Begin itself only records the transition and returns immutable metadata.
func (t *Tx) Begin(id, encoded uint64) (Attempt, error) {
	it := t.items[id]
	if it == nil {
		return Attempt{}, ErrUnknown
	}
	if it.stage != Reserved {
		return Attempt{}, ErrState
	}
	return t.begin(it, it.account, encoded, false)
}
func (t *Tx) BeginRepair(id uint64, account string, encoded, start, end uint64) (Attempt, error) {
	it := t.items[id]
	if it == nil {
		return Attempt{}, ErrUnknown
	}
	if (it.stage != Writing && it.stage != VisibleWait) || it.started == 0 ||
		start < it.offset || end > it.offset+uint64(len(it.payload)) || start >= end {
		return Attempt{}, ErrState
	}
	return t.begin(it, account, encoded, true)
}

// FrameInfo exposes immutable scalar metadata without copying queued payload.
func (t *Tx) FrameInfo(id uint64) (wire.Frame, uint64, error) {
	it := t.items[id]
	if it == nil {
		return wire.Frame{}, 0, ErrUnknown
	}
	return wire.Frame{Type: wire.Data, Stream: it.stream, Logical: it.id, Offset: it.offset}, uint64(len(it.payload)), nil
}

func (t *Tx) Frame(id uint64) (wire.Frame, error) {
	it := t.items[id]
	if it == nil {
		return wire.Frame{}, ErrUnknown
	}
	return wire.Frame{Type: wire.Data, Stream: it.stream, Logical: it.id, Offset: it.offset,
		Payload: append([]byte(nil), it.payload...)}, nil
}
func (t *Tx) Complete(id uint64, outcome Outcome) error {
	if outcome < Committed || outcome > CommitUnknown {
		return ErrState
	}
	a := t.attempts[id]
	if a == nil {
		if id > 0 && id < t.nextAttempt {
			return nil
		} // already physically retired
		return ErrUnknown
	}
	if a.Outcome != Pending {
		if a.Outcome == outcome {
			return nil
		}
		if a.Outcome != CommitUnknown || outcome != Committed {
			return ErrConflict
		}
	}
	if a.Cleaned && outcome == NotCommitted {
		return ErrConflict
	}
	a.Outcome = outcome
	if it := t.items[a.Item]; it != nil {
		// Proven pre-APPEND yield is scheduling, not a physical retry. Unknown
		// outcomes retain the original bounded attempt budget and physical debt.
		if outcome == NotCommitted {
			it.started--
		}
		if outcome == Committed {
			it.provenCommitted = true
		}
		possible := it.provenCommitted
		for _, b := range t.attempts {
			if b.Item == it.id && b.Outcome != NotCommitted {
				possible = true
				break
			}
		}
		if !possible {
			t.debt[it.account] -= uint64(len(it.payload))
			it.account = ""
			it.stage = Queued
		} else {
			it.stage = VisibleWait
		}
	}
	if outcome == NotCommitted || a.Cleaned {
		delete(t.attempts, id)
	}
	return nil
}

// Cleanup is an authenticated/locally verified fact, not a timeout or RECEIPT.
func (t *Tx) Cleanup(id uint64) error {
	a := t.attempts[id]
	if a == nil {
		if id > 0 && id < t.nextAttempt {
			return nil
		}
		return ErrUnknown
	}
	a.Cleaned = true
	if it := t.items[a.Item]; it != nil {
		it.provenCommitted = true
	}
	if a.Outcome != Pending {
		delete(t.attempts, id)
	}
	return nil
}
func (t *Tx) Receipt(stream, frontier uint64) (uint64, error) {
	s := t.streams[stream]
	if s == nil {
		return 0, ErrUnknown
	}
	if frontier <= s.receipt {
		return 0, nil
	}
	if frontier > s.next {
		return 0, ErrReceipt
	}
	covered := []uint64{}
	boundary := false
	for id, it := range t.items {
		if it.stream != stream || it.offset >= frontier {
			continue
		}
		end := it.offset + uint64(len(it.payload))
		if end > frontier || it.started == 0 || it.stage == Queued || it.stage == Reserved {
			return 0, ErrReceipt
		}
		if end == frontier {
			boundary = true
		}
		covered = append(covered, id)
	}
	if !boundary {
		return 0, ErrReceipt
	}
	var released uint64
	for _, id := range covered {
		released += t.release(id)
	}
	s.receipt = frontier
	return released, nil
}
func (t *Tx) release(id uint64) uint64 {
	it := t.items[id]
	n := uint64(len(it.payload))
	t.logical -= n
	if it.account != "" {
		t.debt[it.account] -= n
	}
	clear(it.payload)
	delete(t.items, id)
	return n
}

// Reset does not erase physical attempts. Their completion/cleanup may arrive later.
func (t *Tx) ForgetStream(stream uint64) error {
	if t.streams[stream] == nil {
		return ErrUnknown
	}
	for id, it := range t.items {
		if it.stream == stream {
			t.release(id)
		}
	}
	delete(t.streams, stream)
	return nil
}
func (t *Tx) IsQueued(id uint64) bool { it := t.items[id]; return it != nil && it.stage == Queued }

func (t *Tx) PendingIDs() []uint64 {
	ids := make([]uint64, 0, len(t.items))
	for id := range t.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
func (t *Tx) Snapshot() TxSnapshot {
	s := TxSnapshot{Logical: t.logical, Items: len(t.items), Attempts: len(t.attempts), AccountDebt: map[string]uint64{}}
	for k, n := range t.debt {
		s.AccountDebt[k] = n
	}
	for _, it := range t.items {
		s.Stages[it.stage] += uint64(len(it.payload))
	}
	for _, a := range t.attempts {
		if !a.Cleaned && a.Outcome != NotCommitted {
			s.PhysicalExposure += a.EncodedBytes
		}
	}
	return s
}
func (t *Tx) Check() error {
	var total uint64
	debts := map[string]uint64{}
	for _, it := range t.items {
		n := uint64(len(it.payload))
		total += n
		if it.stage > VisibleWait || t.streams[it.stream] == nil || n == 0 {
			return ErrState
		}
		if it.account != "" {
			debts[it.account] += n
		} else if it.stage != Queued {
			return ErrState
		}
	}
	if total != t.logical || total > t.limits.Logical || len(t.items) > t.limits.Items || len(t.attempts) > t.limits.Attempts || t.peerConsumed > t.peerMaxData {
		return ErrCredit
	}
	for a, n := range t.debt {
		if n != debts[a] || n > t.caps[a] {
			return fmt.Errorf("%w: account debt", ErrCredit)
		}
	}
	return nil
}

// UpdateCredit applies independently monotonic absolute limits atomically.
// Reordered snapshots may advance either dimension; stale values never shrink
// a window, and invalid values cannot partially update connection credit.
func (t *Tx) UpdateCredit(stream uint64, credit wire.Credit) error {
	s := t.streams[stream]
	if s == nil {
		return ErrUnknown
	}
	if credit.MaxStream > wire.MaxOffset || credit.MaxData > wire.MaxOffset {
		return ErrCredit
	}
	if credit.MaxStream > s.peerMax {
		s.peerMax = credit.MaxStream
	}
	if credit.MaxData > t.peerMaxData {
		t.peerMaxData = credit.MaxData
	}
	return nil
}

// AttemptedEnd includes all DATA handed to a writer, even unknown commits and
// ranges already receipted. Queued bytes alone are not advertised as attempted.
func (t *Tx) AttemptedEnd(stream uint64) (uint64, error) {
	s := t.streams[stream]
	if s == nil {
		return 0, ErrUnknown
	}
	return s.attempted, nil
}

// AdmissionCapacity is exact. Repeatedly halving a socket read against an odd
// credit window creates a train of tiny records, each with its own mail grant.
func (t *Tx) AdmissionCapacity(stream uint64) uint64 {
	return t.AdmissionCapacityReserved(stream, 0)
}

// Reserve shared credit for short reads without changing protocol windows.
func (t *Tx) AdmissionCapacityReserved(stream, reserve uint64) uint64 {
	s := t.streams[stream]
	if s == nil || len(t.items) >= t.limits.Items || t.nextItem == 0 || s.next > s.peerMax || t.peerConsumed > t.peerMaxData {
		return 0
	}
	logical, connection := t.limits.Logical-t.logical, t.peerMaxData-t.peerConsumed
	if logical <= reserve || connection <= reserve {
		return 0
	}
	return min(uint64(wire.MaxData), logical-reserve, connection-reserve, s.peerMax-s.next, wire.MaxOffset-s.next)
}

// PendingStreamBytes counts admitted payload awaiting authenticated receipt.
// Physical cleanup does not change this logical outstanding-byte frontier.
func (t *Tx) PendingStreamBytes(id uint64) uint64 {
	if s := t.streams[id]; s != nil {
		return s.next - s.receipt
	}
	return 0
}

// Scalar actor-owned counters avoid copying physical proof state for scheduling.
func (t *Tx) LogicalBytes() uint64 { return t.logical }
func (t *Tx) ItemCount() int       { return len(t.items) }
func (t *Tx) AttemptCount() int    { return len(t.attempts) }

func (t *Tx) StreamFrontier(id uint64) (next, receipt uint64) {
	if s := t.streams[id]; s != nil {
		return s.next, s.receipt
	}
	return 0, 0
}
