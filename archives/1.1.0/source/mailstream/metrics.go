package mailstream

import "time"

func measuredMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return max(1, d.Milliseconds())
}

type deliverySample struct {
	id       SessionID
	sequence uint64
	started  time.Time
	retried  bool
}

// The route belongs to the actual account chosen by Send, not an estimate
// copied from the global session ping. Receipts authenticate delivery; empty-FETCH
// timings independently measure the direct client-to-provider command path.
func (a *mailAccountState) sentPacket(packet []byte) {
	kind, id, seq, err := parseHeader(packet)
	if err != nil || kind != sealed {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	prior := &a.delivery[seq%replaySlots]
	if prior.id == id && prior.sequence == seq && !prior.started.IsZero() {
		prior.retried = true
	} else {
		*prior = deliverySample{id: id, sequence: seq, started: time.Now()}
	}
}

func (l *IMAPLink) TrackStream(id SessionID, sequence uint64, stream uint32) {
	if stream == 0 {
		return
	}
	for _, a := range l.accounts {
		a.mu.Lock()
		sample := a.delivery[sequence%replaySlots]
		if a.id == id && sample.id == id && sample.sequence == sequence {
			a.streams[stream] = true
		}
		a.mu.Unlock()
	}
}

func (l *IMAPLink) AccountStreams(account string, active map[uint32]bool) int {
	for _, a := range l.accounts {
		if a.config.ID != account {
			continue
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		n := 0
		for stream := range a.streams {
			if active[stream] {
				n++
			} else {
				delete(a.streams, stream)
			}
		}
		return n
	}
	return 0
}

func (s *Session) ActiveStreams() map[uint32]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := make(map[uint32]bool, len(s.streams))
	for id := range s.streams {
		active[id] = true
	}
	return active
}
