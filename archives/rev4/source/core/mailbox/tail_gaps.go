package mailbox

import "time"

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
	validity uint32
	next     time.Time
	queue    []tailGap
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
	}
	return snapshot
}
func (c *Client) RestoreTailDiscovery(snapshot TailDiscovery) error {
	if !c.folders[snapshot.folder] || len(snapshot.state.queue) > tailGapLimit {
		return ErrScope
	}
	c.tailGapMu.Lock()
	defer c.tailGapMu.Unlock()
	if c.tailGaps == nil {
		c.tailGaps = make(map[string]*tailGapState)
	}
	s := snapshot.state
	s.queue = append([]tailGap(nil), s.queue...)
	c.tailGaps[snapshot.folder] = &s
	return nil
}
