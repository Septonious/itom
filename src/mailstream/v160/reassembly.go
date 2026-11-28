package v160

import (
	"bytes"
	"io"
	"math"
	"sort"
	"time"
)

// Reassembler has a single stream-lock owner. It stores disjoint ranges, uses
// binary search for overlap lookup, and never copies an already buffered byte
// merely to merge adjacent ranges. Capacity is checked before committing any
// change. A session-level reserve callback provides the aggregate memory fence.
const MaxRanges = 4096

type chunk struct {
	at   uint64
	data []byte
}

func (c chunk) end() uint64 { return c.at + uint64(len(c.data)) }

type ReassemblyStats struct {
	Consumed, ReadyEnd, Highest      uint64
	Buffered, Ready, Blocked, Ranges int
	GapOffset, GapEnd                uint64
	GapSince                         time.Time
	DuplicateBytes                   uint64
}
type Reassembler struct {
	chunks                      []chunk
	consumed, readyEnd, highest uint64
	buffered                    int
	final                       uint64
	hasFinal                    bool
	gapSince                    time.Time
	gapOffset                   uint64
	duplicates                  uint64
}

func (r *Reassembler) Stats() ReassemblyStats {
	ready := int(r.readyEnd - r.consumed)
	s := ReassemblyStats{Consumed: r.consumed, ReadyEnd: r.readyEnd, Highest: r.highest, Buffered: r.buffered, Ready: ready, Blocked: r.buffered - ready, Ranges: len(r.chunks), GapSince: r.gapSince, DuplicateBytes: r.duplicates}
	if !r.gapSince.IsZero() {
		s.GapOffset = r.readyEnd
		i := sort.Search(len(r.chunks), func(i int) bool { return r.chunks[i].at > r.readyEnd })
		if i < len(r.chunks) {
			s.GapEnd = r.chunks[i].at
		} else if r.hasFinal {
			s.GapEnd = r.final
		}
	}
	return s
}
func (r *Reassembler) updateGap(now time.Time) {
	if r.buffered == int(r.readyEnd-r.consumed) && (!r.hasFinal || r.final == r.readyEnd) {
		r.gapSince = time.Time{}
		r.gapOffset = 0
		return
	}
	if r.gapSince.IsZero() || r.gapOffset != r.readyEnd {
		r.gapSince = now
		r.gapOffset = r.readyEnd
	}
}
func (r *Reassembler) Add(offset uint64, data []byte, limit int, now time.Time, reserve func(int) bool) (int, error) {
	if len(data) == 0 || limit < 0 || offset > math.MaxUint64-uint64(len(data)) {
		return 0, ErrProtocol
	}
	end := offset + uint64(len(data))
	original := len(data)
	if end <= r.consumed {
		r.duplicates += uint64(original)
		return 0, nil
	}
	if end-r.consumed > uint64(limit) || r.hasFinal && end > r.final {
		return 0, ErrProtocol
	}
	if offset < r.consumed {
		data = data[r.consumed-offset:]
		offset = r.consumed
	}
	i := sort.Search(len(r.chunks), func(i int) bool { return r.chunks[i].end() > offset })
	// Construct views of only the uncovered fragments; compare all overlapping
	// authenticated data first. Nothing is mutated on conflicting bytes.
	cursor := offset
	var add []chunk
	unique := 0
	for j := i; j < len(r.chunks) && r.chunks[j].at < end; j++ {
		c := r.chunks[j]
		if cursor < c.at {
			stop := min(c.at, end)
			p := data[cursor-offset : stop-offset]
			add = append(add, chunk{cursor, p})
			unique += len(p)
			cursor = stop
		}
		lo, hi := max(offset, c.at), min(end, c.end())
		if lo < hi && !bytes.Equal(data[lo-offset:hi-offset], c.data[lo-c.at:hi-c.at]) {
			return 0, ErrProtocol
		}
		cursor = max(cursor, hi)
	}
	if cursor < end {
		p := data[cursor-offset:]
		add = append(add, chunk{cursor, p})
		unique += len(p)
	}
	if unique == 0 {
		r.duplicates += uint64(original)
		return 0, nil
	}
	if unique > limit-r.buffered || len(r.chunks)+len(add) > MaxRanges {
		return 0, ErrCapacity
	}
	if reserve != nil && !reserve(unique) {
		return 0, ErrCapacity
	}
	// Insert from high to low. A common in-order frame only appends, with no
	// full-range scan or per-frame rebuild of existing storage.
	for j := len(add) - 1; j >= 0; j-- {
		c := add[j]
		c.data = bytes.Clone(c.data)
		at := sort.Search(len(r.chunks), func(i int) bool { return r.chunks[i].at >= c.at })
		r.chunks = append(r.chunks, chunk{})
		copy(r.chunks[at+1:], r.chunks[at:])
		r.chunks[at] = c
	}
	r.buffered += unique
	r.highest = max(r.highest, end)
	r.duplicates += uint64(original - unique)
	if offset <= r.readyEnd {
		at := sort.Search(len(r.chunks), func(i int) bool { return r.chunks[i].end() > r.readyEnd })
		for at < len(r.chunks) && r.chunks[at].at <= r.readyEnd {
			r.readyEnd = r.chunks[at].end()
			at++
		}
	}
	r.updateGap(now)
	return unique, nil
}
func (r *Reassembler) SetFinal(offset uint64, limit int, now time.Time) error {
	if limit < 0 || offset < r.consumed || offset-r.consumed > uint64(limit) || offset < r.highest || r.hasFinal && offset != r.final {
		return ErrProtocol
	}
	r.final = offset
	r.hasFinal = true
	r.updateGap(now)
	return nil
}
func (r *Reassembler) Finished() bool { return r.hasFinal && r.consumed == r.final }

// Read is nonblocking; zero bytes with nil error means a missing offset or an
// empty stream. The caller owns wait/deadline/cancellation and credit release.
func (r *Reassembler) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if r.Finished() {
		return 0, io.EOF
	}
	n := 0
	for len(dst) > 0 && len(r.chunks) > 0 && r.chunks[0].at == r.consumed {
		c := &r.chunks[0]
		k := copy(dst, c.data)
		n += k
		dst = dst[k:]
		c.at += uint64(k)
		c.data = c.data[k:]
		r.consumed += uint64(k)
		r.buffered -= k
		if len(c.data) == 0 {
			r.chunks[0] = chunk{}
			r.chunks = r.chunks[1:]
		}
	}
	return n, nil
}

// Discard releases retained storage after an explicit stream abort. The
// consumed/final offsets are not advanced and no delivery is claimed.
func (r *Reassembler) Discard() int {
	n := r.buffered
	r.chunks = nil
	r.buffered = 0
	r.readyEnd = r.consumed
	r.gapSince = time.Time{}
	r.gapOffset = 0
	return n
}
