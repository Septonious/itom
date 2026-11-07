package mailstream

import "encoding/binary"

// The first 64 KiB of each of at most 64 streams has its own small receive
// allowance. Bulk shares the remaining 60 MiB. Credit follows actual consumed
// stream offsets; mailbox packet receipts do not free application credit.
const streamPrefix = 64 << 10
const bulkBudget = 60 << 20
const receiveMemoryLimit = 64 << 20

type terminalStream struct {
	state          closeState
	sent, consumed uint64
}

func bulkBytes(offset uint64) uint64 {
	if offset <= streamPrefix {
		return 0
	}
	return offset - streamPrefix
}

// Caller holds Session.mu. Reordered WINDOW updates are monotonic and cannot
// refund the same bytes twice, including after a graceful local Close.
func (s *Session) refundBulkLocked(before, after uint64) error {
	if after < before {
		return ErrProtocol
	}
	delta := bulkBytes(after) - bulkBytes(before)
	if delta > s.bulkOutstanding {
		return ErrProtocol
	}
	s.bulkOutstanding -= delta
	s.notifyLocked()
	return nil
}
func (s *Session) releaseTerminalLocked(id uint32) error {
	t := s.closedStreams[id]
	if t == nil {
		return nil
	}
	if err := s.refundBulkLocked(t.consumed, t.sent); err != nil {
		return err
	}
	t.consumed = t.sent
	return nil
}
func (s *Stream) consumedLocked(offset uint64) error {
	if offset > s.sendOffset {
		return ErrProtocol
	}
	offset = max(offset, s.peerConsumed)
	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if err := s.session.refundBulkLocked(s.peerConsumed, offset); err != nil {
		return err
	}
	s.peerConsumed = offset
	return nil
}
func (s *Session) closedFrameLocked(t *terminalStream, f frame) error {
	switch f.kind {
	case frameWindow:
		if len(f.data) != 4 || f.offset > t.sent {
			return ErrProtocol
		}
		window := binary.BigEndian.Uint32(f.data)
		if window < initialStreamWindow || window > streamWindow {
			return ErrProtocol
		}
		consumed := max(t.consumed, f.offset)
		if err := s.refundBulkLocked(t.consumed, consumed); err != nil {
			return err
		}
		t.consumed = consumed
	case frameReset:
		if len(f.data) != 0 || f.offset != 0 {
			return ErrProtocol
		}
		if err := s.refundBulkLocked(t.consumed, t.sent); err != nil {
			return err
		}
		t.consumed = t.sent
	}
	return nil
}
func (s *Session) reserveReceive(n int) bool {
	for {
		current := s.receiveBuffered.Load()
		if current+int64(n) > receiveMemoryLimit {
			return false
		}
		if s.receiveBuffered.CompareAndSwap(current, current+int64(n)) {
			return true
		}
	}
}
