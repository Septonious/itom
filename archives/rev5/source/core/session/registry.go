// Package session contains actor-owned, in-memory session primitives. Session
// identity must be fresh after restart; a closed stream ID is never reused.
package session

import (
	"errors"
	"sort"

	"itom.local/core/wire"
)

var (
	ErrClosed   = errors.New("STREAM_CLOSED")
	ErrCapacity = errors.New("STREAM_CAPACITY")
	ErrID       = errors.New("STREAM_ID")
	ErrState    = errors.New("SESSION_STATE")
)

type interval struct{ first, last uint64 }
type Registry struct {
	active                 map[uint64]struct{}
	closed                 []interval
	maxActive, maxMetadata int
}

func NewRegistry(maxActive, maxMetadata int) (*Registry, error) {
	if maxActive < 1 || maxActive > 4096 || maxMetadata < maxActive || maxMetadata > 65536 {
		return nil, ErrCapacity
	}
	return &Registry{active: map[uint64]struct{}{}, maxActive: maxActive, maxMetadata: maxMetadata}, nil
}
func (r *Registry) IsClosed(id uint64) bool {
	i := sort.Search(len(r.closed), func(i int) bool { return r.closed[i].last >= id })
	return i < len(r.closed) && r.closed[i].first <= id
}

// Open permits reordered IDs. Metadata reserves one future tombstone for each
// active stream, so Close can never fail for lack of tombstone space.
func (r *Registry) Open(id uint64) (bool, error) {
	if id == 0 || id%2 != 1 || id > wire.MaxOffset {
		return false, ErrID
	}
	if r.IsClosed(id) {
		return false, ErrClosed
	}
	if _, ok := r.active[id]; ok {
		return true, nil
	}
	if len(r.active) >= r.maxActive || len(r.active)+len(r.closed) >= r.maxMetadata {
		return false, ErrCapacity
	}
	r.active[id] = struct{}{}
	return false, nil
}
func (r *Registry) Close(id uint64) error {
	if r.IsClosed(id) {
		return nil
	}
	if _, ok := r.active[id]; !ok {
		return ErrID
	}
	delete(r.active, id)
	r.recordClosed(id)
	return nil
}

// Reject permanently records an unadmitted OPEN without consuming an active
// socket. If metadata itself is full, fail closed rather than forgetting the ID.
func (r *Registry) Reject(id uint64) error {
	if id == 0 || id%2 != 1 || id > wire.MaxOffset {
		return ErrID
	}
	if r.IsClosed(id) {
		return nil
	}
	if _, exists := r.active[id]; exists {
		return ErrState
	}
	if len(r.active)+len(r.closed) >= r.maxMetadata {
		return ErrCapacity
	}
	r.recordClosed(id)
	return nil
}

func (r *Registry) recordClosed(id uint64) {
	r.closed = append(r.closed, interval{id, id})
	sort.Slice(r.closed, func(i, j int) bool { return r.closed[i].first < r.closed[j].first })
	merged := r.closed[:0]
	for _, x := range r.closed {
		if len(merged) > 0 && x.first-merged[len(merged)-1].last <= 2 {
			merged[len(merged)-1].last = x.last
		} else {
			merged = append(merged, x)
		}
	}
	r.closed = merged
}
func (r *Registry) Counts() (int, int) { return len(r.active), len(r.closed) }

type Phase uint8

const (
	Offline Phase = iota
	Handshaking
	Confirming
	Active
	Stopped
)

type Lifecycle struct {
	generation uint64
	phase      Phase
}

func (l *Lifecycle) Start() (uint64, error) {
	if l.phase != Offline && l.phase != Stopped {
		return 0, ErrState
	}
	if l.generation == ^uint64(0) {
		return 0, ErrState
	}
	l.generation++
	l.phase = Handshaking
	return l.generation, nil
}
func (l *Lifecycle) Advance(generation uint64, from, to Phase) error {
	if generation != l.generation || from != l.phase ||
		!((from == Handshaking && to == Confirming) || (from == Confirming && to == Active)) {
		return ErrState
	}
	l.phase = to
	return nil
}
func (l *Lifecycle) Stop() { l.phase = Stopped }
func (l *Lifecycle) Accept(generation uint64) bool {
	return generation == l.generation && (l.phase == Handshaking || l.phase == Confirming || l.phase == Active)
}
func (l *Lifecycle) Ready() bool { return l.phase == Active }
