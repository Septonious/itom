package mailstream

import (
	"context"
	"time"
)

type accountProbe struct {
	account string
	done    chan struct{}
}

// PingAccount measures one authenticated round trip on the named physical
// mailbox. Starting the clock before admission includes local queue delay.
// Probes have no retransmission or hedge. A lost reply expires its own sample.
func (s *Session) PingAccount(ctx context.Context, account string) (time.Duration, error) {
	link, ok := s.link.(*IMAPLink)
	if !ok || !validIdentity(account) || link.account(account) == nil {
		return 0, ErrProtocol
	}
	start := time.Now()
	s.mu.Lock()
	if len(s.accountPings) >= controlReserve {
		s.mu.Unlock()
		return 0, ErrCapacity
	}
	if s.accountPings == nil {
		s.accountPings = make(map[uint64]accountProbe)
	}
	s.nextPing++
	token := s.nextPing
	done := make(chan struct{})
	s.accountPings[token] = accountProbe{account: account, done: done}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.accountPings, token); s.mu.Unlock() }()
	if err := s.sendAccountProbe(ctx, link, account, frameAccountPing, token); err != nil {
		return 0, err
	}
	select {
	case <-done:
		return time.Since(start), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.ctx.Done():
		return 0, ErrClosed
	}
}

func (s *Session) sendAccountProbe(ctx context.Context, link *IMAPLink, account string, kind byte, token uint64) error {
	body, err := (frame{kind: kind, offset: token, data: []byte(account)}).encode()
	if err != nil {
		return err
	}
	packet, _, err := s.cipher.seal(body)
	if err != nil {
		return err
	}
	return link.sendOnAccount(ctx, s.cipher.id, account, packet, controlMessageLimit)
}

func (s *Session) handleAccountProbe(f frame, seq uint64) {
	link, ok := s.link.(*IMAPLink)
	if !ok || f.stream != 0 || !validIdentity(string(f.data)) || f.offset == 0 {
		return
	}
	account := string(f.data)
	link.mu.Lock()
	received := link.receivedRoutes[receivedRoute{s.cipher.id, seq}]
	link.mu.Unlock()
	if received != account {
		return
	}
	if s.seenValid[seq%replaySlots] && s.seen[seq%replaySlots] == seq {
		return
	}
	s.seen[seq%replaySlots], s.seenValid[seq%replaySlots] = seq, true
	if f.kind == frameAccountPing {
		// Admission failure, including maintenance backpressure, is visible
		// to the sender as a missed probe. Never move a reply to another path.
		_ = s.sendAccountProbe(s.ctx, link, account, frameAccountPong, f.offset)
		return
	}
	s.mu.Lock()
	if waiting, ok := s.accountPings[f.offset]; ok && waiting.account == account {
		delete(s.accountPings, f.offset)
		close(waiting.done)
	}
	s.mu.Unlock()
}
