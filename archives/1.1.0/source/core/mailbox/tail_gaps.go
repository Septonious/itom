package mailbox

import (
	"sort"
	"time"
)

// Discovery only: these bounds do not authorize quota refunds or DATA retries.
// Long visibility outages still require protocol reconciliation/reconnection.
const tailGapLimit = 1024
const tailGapLifetime = 30 * time.Second
const tailGapPoll = 250 * time.Millisecond

type tailGap struct {
	uid   uint32
	until time.Time
}
type tailGapState struct {
	validity  uint32
	next      time.Time
	queue     []tailGap
	ranges    []tailRange
	rangeNext time.Time
}

// Sparse historical UID spans cannot be represented by one entry per deleted
// UID. These intervals are discovery hints with the same finite lifetime as
// exact gaps; SEARCH absence never authorizes deletion or a quota refund.
type tailRange struct {
	first, last uint32
	until       time.Time
}

func missingTailRanges(first, last uint32, rows []Message, until time.Time) []tailRange {
	if first == 0 || last < first {
		return nil
	}
	ids := make([]uint32, 0, len(rows))
	for _, row := range rows {
		if row.UID >= first && row.UID <= last {
			ids = append(ids, row.UID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []tailRange
	start := uint64(first)
	for _, id := range ids {
		if uint64(id) < start {
			continue
		}
		if uint64(id) > start {
			out = append(out, tailRange{uint32(start), id - 1, until})
		}
		start = uint64(id) + 1
	}
	if start <= uint64(last) {
		out = append(out, tailRange{uint32(start), last, until})
	}
	return out
}

func (c *Client) sparseTailRequest(folder string, validity uint32, now time.Time) (tailRange, bool) {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	s := c.tailGapState(folder, validity)
	live := s.ranges[:0]
	for _, span := range s.ranges {
		if now.Before(span.until) {
			live = append(live, span)
		}
	}
	s.ranges = live
	if now.Before(s.rangeNext) || len(live) == 0 {
		return tailRange{}, false
	}
	return live[0], true
}

func (c *Client) finishSparseTail(folder string, validity uint32, requested *tailRange, through uint32, rows []Message, now time.Time) {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	s := c.tailGapState(folder, validity)
	if requested != nil {
		// Rotate the searched prefix behind the unsearched suffix. A permanently
		// missing BODY or old expunged span cannot starve the next visible UID.
		span := *requested
		s.ranges = s.ranges[1:]
		if through < span.last {
			s.ranges = append([]tailRange{{through + 1, span.last, span.until}}, s.ranges...)
		}
		s.ranges = append(s.ranges, missingTailRanges(span.first, through, rows, span.until)...)
		s.rangeNext = now.Add(tailGapPoll)
	}
	if len(s.ranges) > tailGapLimit {
		s.ranges = append([]tailRange(nil), s.ranges[len(s.ranges)-tailGapLimit:]...)
	}
}

func (c *Client) trackSparseTail(folder string, validity, first, last uint32, rows []Message, now time.Time) {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	s := c.tailGapState(folder, validity)
	s.ranges = append(s.ranges, missingTailRanges(first, last, rows, now.Add(tailGapLifetime))...)
	if len(s.ranges) > tailGapLimit {
		s.ranges = append([]tailRange(nil), s.ranges[len(s.ranges)-tailGapLimit:]...)
	}
}

// TrackTailGaps covers the reader’s own allocation snapshot. Call only after
// successful tagged completion of the bounded FETCH.
func (c *Client) TrackTailGaps(folder string, cur, next Cursor, rows []Message) {
	c.trackTailGaps(folder, cur, next, rows, time.Now())
}

func (c *Client) tailGapState(folder string, validity uint32) *tailGapState {
	if c.tailGaps == nil {
		c.tailGaps = make(map[string]*tailGapState)
	}
	s := c.tailGaps[folder]
	if s == nil || s.validity != validity {
		s = &tailGapState{validity: validity}
		c.tailGaps[folder] = s
	}
	return s
}

func (c *Client) trackTailGaps(folder string, cur, next Cursor, rows []Message, now time.Time) {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	if next.Validity == 0 || next.Validity != cur.Validity || next.After <= cur.After || next.After-cur.After > 16 {
		return
	}
	s := c.tailGapState(folder, next.Validity)
	present := make(map[uint32]bool, int(next.After-cur.After))
	for _, m := range rows {
		if m.UID > cur.After && m.UID <= next.After {
			present[m.UID] = true
		}
	}
	for _, gap := range s.queue {
		if gap.uid > cur.After && gap.uid <= next.After {
			present[gap.uid] = true
		}
	}
	for uid := cur.After + 1; ; uid++ {
		if !present[uid] {
			s.queue = append(s.queue, tailGap{uid: uid, until: now.Add(tailGapLifetime)})
		}
		if uid == next.After {
			break
		}
	}
	if len(s.queue) > tailGapLimit {
		copy(s.queue, s.queue[len(s.queue)-tailGapLimit:])
		s.queue = s.queue[:tailGapLimit]
	}
}

func (c *Client) tailGapRequests(folder string, validity uint32, limit int, now time.Time) []tailGap {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	s := c.tailGapState(folder, validity)
	if now.Before(s.next) || len(s.queue) == 0 {
		return nil
	}
	live := s.queue[:0]
	for _, gap := range s.queue {
		if now.Before(gap.until) {
			live = append(live, gap)
		}
	}
	s.queue = live
	n := min(limit, len(live))
	if n == 0 {
		return nil
	}
	return append([]tailGap(nil), live[:n]...)
}

func (c *Client) finishTailGapRequests(folder string, validity uint32, gaps []tailGap, rows []Message, now time.Time) {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	if len(gaps) == 0 {
		return
	}
	s := c.tailGapState(folder, validity)
	found := make(map[uint32]bool, len(rows))
	for _, m := range rows {
		found[m.UID] = true
	}
	// Rotate unfulfilled requests so permanent deletions cannot starve new gaps.
	s.queue = append(s.queue[:0], s.queue[len(gaps):]...)
	for _, gap := range gaps {
		if !found[gap.uid] {
			s.queue = append(s.queue, gap)
		}
	}
	s.next = now.Add(tailGapPoll)
}

// TailDiscovery is bounded UID discovery state, never a quota or absence proof.
// Copy retains the original expiry; a socket handoff cannot extend visibility TTL.
type TailDiscovery struct {
	folder string
	state  tailGapState
}

func (c *Client) TailDiscovery(folder string) TailDiscovery {
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	snapshot := TailDiscovery{folder: folder}
	if s := c.tailGaps[folder]; s != nil {
		snapshot.state = *s
		snapshot.state.queue = append([]tailGap(nil), s.queue...)
		snapshot.state.ranges = append([]tailRange(nil), s.ranges...)
	}
	return snapshot
}
func (c *Client) RestoreTailDiscovery(snapshot TailDiscovery) error {
	if !c.folders[snapshot.folder] || len(snapshot.state.queue) > tailGapLimit || len(snapshot.state.ranges) > tailGapLimit {
		return ErrScope
	}
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	if c.tailGaps == nil {
		c.tailGaps = make(map[string]*tailGapState)
	}
	s := snapshot.state
	s.queue = append([]tailGap(nil), s.queue...)
	s.ranges = append([]tailRange(nil), s.ranges...)
	c.tailGaps[snapshot.folder] = &s
	return nil
}
