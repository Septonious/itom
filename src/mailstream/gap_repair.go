package mailstream

import (
	"context"
	"encoding/binary"
	"sort"
	"time"
)

const gapReportInterval = 100 * time.Millisecond
const maxGapReportBytes = 2 * maxPayload

// A selective gap is a hint, not a receipt or credit grant. It may only
// expedite an already-sent unacknowledged immutable DATA record. Every repair
// is still charged to the ordinary writer/pacer and physical mailbox budget.
func (s *Session) canRepairLocked(p *pendingPacket) bool {
	return p.repairRequested && !p.earlyRepaired && p.attempts == 1 && !p.priority && s.repairCredit >= len(p.wire)
}
func (s *Session) acceptGap(f frame) error {
	if f.stream == 0 || f.stream%2 == 0 || f.stream > 131071 || len(f.data) != 8 {
		return ErrProtocol
	}
	end := binary.BigEndian.Uint64(f.data)
	if end <= f.offset || end-f.offset > maxGapReportBytes {
		return ErrProtocol
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Bound a hint to two intersecting records, in offset order. Normal
	// timeout recovery remains available if a path or repair budget is absent.
	var choices [2]*pendingPacket
	now := time.Now()
	for _, p := range s.pending {
		if p.kind != frameData || p.stream != f.stream || p.priority || p.attempts != 1 || p.earlyRepaired || p.repairRequested || p.payloadBytes <= 0 || p.offset >= end || p.offset+uint64(p.payloadBytes) <= f.offset || now.Sub(p.sent) < 40*time.Millisecond {
			continue
		}
		if choices[0] == nil || p.offset < choices[0].offset {
			choices[1] = choices[0]
			choices[0] = p
		} else if choices[1] == nil || p.offset < choices[1].offset {
			choices[1] = p
		}
	}
	for _, p := range choices {
		if p != nil {
			p.repairRequested = true
			s.repairRequests++
		}
	}
	s.notifyLocked()
	return nil
}

func (s *Session) gapLoop() {
	// Only a carrier that can select an alternate path benefits from hints.
	// Test and other Links can implement the same small optional contract.
	if _, ok := s.link.(interface {
		SendRepair(context.Context, []byte) error
	}); !ok {
		return
	}
	tick := time.NewTicker(gapReportInterval)
	defer tick.Stop()
	var cursor uint32
	streams := make([]*Stream, 0, maxStreams)
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-tick.C:
			s.mu.Lock()
			streams = streams[:0]
			for _, st := range s.streams {
				streams = append(streams, st)
			}
			delay := min(500*time.Millisecond, max(120*time.Millisecond, 2*s.variation+40*time.Millisecond))
			s.mu.Unlock()
			sort.Slice(streams, func(i, j int) bool { return streams[i].id-cursor-1 < streams[j].id-cursor-1 })
			reports := 0
			for _, st := range streams {
				if st.reportGap(now, delay) {
					cursor = st.id
					reports++
					if reports >= 4 {
						break
					}
				}
			}
		}
	}
}
func (st *Stream) reportGap(now time.Time, delay time.Duration) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	g := st.receive.Stats()
	if st.err != nil || g.GapSince.IsZero() || now.Sub(g.GapSince) < delay || g.GapEnd <= g.GapOffset {
		return false
	}
	if st.gapReportedAt.IsZero() || st.gapReportedOffset != g.GapOffset {
		st.gapReports = 0
		st.gapReportedOffset = g.GapOffset
		st.gapReportedAt = time.Time{}
	}
	if st.gapReports >= 3 || !st.gapReportedAt.IsZero() && now.Sub(st.gapReportedAt) < 300*time.Millisecond {
		return false
	}
	end := g.GapOffset + min(g.GapEnd-g.GapOffset, uint64(maxGapReportBytes))
	if st.session.trySend(frame{kind: frameGap, stream: st.id, offset: g.GapOffset, data: binary.BigEndian.AppendUint64(nil, end)}, true) != nil {
		return false
	}
	st.gapReports++
	st.gapReportedAt = now
	st.session.gapReportsSent.Add(1)
	return true
}

func (l *IMAPLink) SendRepair(ctx context.Context, body []byte) error {
	kind, id, seq, err := parseHeader(body)
	if err != nil || kind != sealed {
		return ErrProtocol
	}
	var previous *mailAccountState
	var at time.Time
	for _, a := range l.accounts {
		a.mu.Lock()
		sample := a.delivery[seq%replaySlots]
		a.mu.Unlock()
		if sample.id == id && sample.sequence == seq && sample.started.After(at) {
			previous = a
			at = sample.started
		}
	}
	if previous == nil {
		return ErrCapacity
	}
	_, err = l.send(ctx, body, false, previous)
	return err
}
