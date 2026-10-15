package flow

import (
	"crypto/sha256"
	"sort"

	"itom.local/core/wire"
)

type segment struct {
	start, originalOffset, originalEnd, logical uint64
	digest                                      [32]byte
	data                                        []byte
}
type rxStream struct {
	max, highest, receipt, delivered uint64
	final                            *uint64
	segments                         []segment
}
type Rx struct {
	cap, maxData, consumed, retired uint64
	maxStreams, maxIntervals        int
	streams                         map[uint64]*rxStream
}
type RxSnapshot struct {
	MaxData, Consumed, Retired, Buffered uint64
	Streams, Intervals                   int
}

// The aggregate receive budget remains authoritative across all streams.
// A stream window is permission, not a permanently allocated byte buffer.
const MaxStreamWindow = 16 << 20

func NewRx(cap uint64, maxStreams, maxIntervals int) (*Rx, error) {
	if cap == 0 || cap > 1<<30 || maxStreams < 1 || maxStreams > 4096 || maxIntervals < 1 || maxIntervals > 65536 {
		return nil, ErrCredit
	}
	return &Rx{cap: cap, maxData: cap, maxStreams: maxStreams, maxIntervals: maxIntervals, streams: map[uint64]*rxStream{}}, nil
}
func (r *Rx) OpenStream(id, window uint64) error {
	if id == 0 || id%2 != 1 || id > wire.MaxOffset || window == 0 || window > MaxStreamWindow {
		return ErrState
	}
	if _, ok := r.streams[id]; ok {
		return ErrState
	}
	if len(r.streams) >= r.maxStreams {
		return ErrCredit
	}
	r.streams[id] = &rxStream{max: window}
	return nil
}
func (r *Rx) UpdateStreamLimit(id, maximum uint64) error {
	s := r.streams[id]
	if s == nil {
		return ErrUnknown
	}
	if maximum < s.max || maximum > wire.MaxOffset || maximum-s.delivered > MaxStreamWindow {
		return ErrCredit
	}
	s.max = maximum
	return nil
}

// Receive accepts only authenticated DATA. Whole immutable records are retained;
// partial/conflicting overlap is a protocol error rather than last-write-wins.
func (r *Rx) Receive(f wire.Frame) (uint64, bool, error) {
	if f.Type != wire.Data {
		return 0, false, ErrState
	}
	if err := f.Validate(); err != nil {
		return 0, false, err
	}
	s := r.streams[f.Stream]
	if s == nil {
		return 0, false, ErrUnknown
	}
	end, _ := f.End()
	if end > s.max || (s.final != nil && end > *s.final) {
		return 0, false, ErrCredit
	}
	if end <= s.delivered {
		return s.receipt, true, nil
	}
	digest := sha256.Sum256(f.Payload)
	for _, seg := range s.segments {
		segEnd := seg.start + uint64(len(seg.data))
		if f.Offset == seg.originalOffset && end == seg.originalEnd {
			if digest != seg.digest || f.Logical != seg.logical {
				return 0, false, ErrConflict
			}
			return s.receipt, true, nil
		}
		if f.Offset < segEnd && end > seg.start {
			return 0, false, ErrConflict
		}
	}
	if f.Offset < s.delivered {
		return 0, false, ErrConflict
	}
	increase := uint64(0)
	if end > s.highest {
		increase = end - s.highest
	}
	if increase > r.maxData-r.consumed {
		return 0, false, ErrCredit
	}
	count := 0
	for _, other := range r.streams {
		count += len(other.segments)
	}
	if count >= r.maxIntervals {
		return 0, false, ErrCredit
	}
	s.segments = append(s.segments, segment{start: f.Offset, originalOffset: f.Offset,
		originalEnd: end, logical: f.Logical, digest: digest, data: append([]byte(nil), f.Payload...)})
	sort.Slice(s.segments, func(i, j int) bool { return s.segments[i].start < s.segments[j].start })
	if end > s.highest {
		s.highest = end
		r.consumed += increase
	}
	cursor := s.receipt
	for _, seg := range s.segments {
		segEnd := seg.start + uint64(len(seg.data))
		if seg.start <= cursor && segEnd > cursor {
			cursor = segEnd
		}
	}
	s.receipt = cursor
	return s.receipt, false, nil
}

// Peek returns a bounded COPY. Delivery only advances by the actual successful
// socket write, including a short write accompanied by an error.
func (r *Rx) Peek(id uint64, max int) ([]byte, error) {
	s := r.streams[id]
	if s == nil {
		return nil, ErrUnknown
	}
	if max < 1 || max > wire.MaxData {
		return nil, ErrCredit
	}
	if len(s.segments) == 0 || s.segments[0].start != s.delivered {
		return nil, nil
	}
	n := len(s.segments[0].data)
	if n > max {
		n = max
	}
	return append([]byte(nil), s.segments[0].data[:n]...), nil
}
func (r *Rx) Delivered(id, n uint64) error {
	s := r.streams[id]
	if s == nil {
		return ErrUnknown
	}
	if n == 0 {
		return nil
	}
	if len(s.segments) == 0 || s.segments[0].start != s.delivered || n > uint64(len(s.segments[0].data)) ||
		r.maxData > wire.MaxOffset-n {
		return ErrState
	}
	seg := &s.segments[0]
	// Copy the remaining tail so delivered prefixes do not pin entire buffers
	// while connection credit is replenished. Allocation is bounded by MaxData.
	rest := append([]byte(nil), seg.data[int(n):]...)
	clear(seg.data)
	seg.data = rest
	seg.start += n
	s.delivered += n
	r.retired += n
	r.maxData += n
	if len(seg.data) == 0 {
		s.segments = s.segments[1:]
	}
	return nil
}
func (r *Rx) Fin(id, final uint64) error {
	s := r.streams[id]
	if s == nil {
		return ErrUnknown
	}
	if final < s.highest || final > s.max {
		return ErrState
	}
	if s.final != nil && *s.final != final {
		return ErrConflict
	}
	value := final
	s.final = &value
	return nil
}
func (r *Rx) HalfClosed(id uint64) bool {
	s := r.streams[id]
	return s != nil && s.final != nil && s.delivered == *s.final
}
func (r *Rx) Frontier(id uint64) (receipt, delivered, limit uint64, err error) {
	s := r.streams[id]
	if s == nil {
		return 0, 0, 0, ErrUnknown
	}
	return s.receipt, s.delivered, s.max, nil
}

// Reset accounts the sender's final offset, including reserved holes, once.
// The outer registry discards duplicate terminal events after this removal.
func (r *Rx) Reset(id, final uint64) error {
	s := r.streams[id]
	if s == nil {
		return ErrUnknown
	}
	if final < s.highest || final > s.max {
		return ErrState
	}
	if s.final != nil && final != *s.final {
		return ErrConflict
	}
	increase := final - s.highest
	if increase > r.maxData-r.consumed {
		return ErrCredit
	}
	retire := final - s.delivered
	if r.maxData > wire.MaxOffset-retire {
		return ErrCredit
	}
	r.consumed += increase
	r.retired += retire
	r.maxData += retire
	for _, seg := range s.segments {
		clear(seg.data)
	}
	delete(r.streams, id)
	return nil
}
func (r *Rx) Snapshot() RxSnapshot {
	s := RxSnapshot{MaxData: r.maxData, Consumed: r.consumed, Retired: r.retired, Streams: len(r.streams)}
	for _, stream := range r.streams {
		s.Intervals += len(stream.segments)
		for _, seg := range stream.segments {
			s.Buffered += uint64(len(seg.data))
		}
	}
	return s
}
func (r *Rx) Check() error {
	s := r.Snapshot()
	if r.retired > r.consumed || r.consumed > r.maxData || r.maxData-r.retired != r.cap ||
		s.Buffered > r.consumed-r.retired || s.Intervals > r.maxIntervals {
		return ErrCredit
	}
	for _, st := range r.streams {
		if st.delivered > st.receipt || st.receipt > st.highest || st.highest > st.max {
			return ErrState
		}
		var end uint64
		for i, seg := range st.segments {
			if len(seg.data) == 0 || seg.start < st.delivered || (i > 0 && seg.start < end) {
				return ErrState
			}
			end = seg.start + uint64(len(seg.data))
		}
	}
	return nil
}

// Missing discovers holes from an authenticated sender STATUS, including a
// lost first, last or ONLY DATA record. It does not reserve credit, advance a
// receipt or issue permission for a sender retry by itself. A capped response
// reports the earliest holes; a later STATUS can discover the remainder.
func (r *Rx) Missing(id, attempted uint64, limit int) ([]wire.ByteRange, error) {
	s := r.streams[id]
	if s == nil {
		return nil, ErrUnknown
	}
	if attempted > s.max || (s.final != nil && attempted > *s.final) || limit < 1 || limit > wire.MaxNackRanges {
		return nil, ErrCredit
	}
	cursor := s.receipt
	if cursor >= attempted {
		return nil, nil
	} // Harmless delayed STATUS.
	ranges := make([]wire.ByteRange, 0, limit)
	for _, seg := range s.segments {
		if seg.start >= attempted {
			break
		}
		end := seg.start + uint64(len(seg.data))
		if end <= cursor {
			continue
		}
		if seg.start > cursor {
			ranges = append(ranges, wire.ByteRange{Start: cursor, End: seg.start})
			if len(ranges) == limit {
				return ranges, nil
			}
		}
		cursor = end
		if cursor >= attempted {
			return ranges, nil
		}
	}
	if cursor < attempted {
		ranges = append(ranges, wire.ByteRange{Start: cursor, End: attempted})
	}
	return ranges, nil
}
