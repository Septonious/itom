package mailstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// Each stream has independent receive storage and absolute consumed-byte
// credit. The mail receive owner never waits for an application Read or dial.
type Stream struct {
	ctx                                              context.Context
	cancel                                           context.CancelFunc
	session                                          *Session
	id                                               uint32
	readMu, writeMu                                  sync.Mutex
	mu                                               sync.Mutex
	changed                                          chan struct{}
	opened                                           chan struct{}
	isOpen                                           bool
	chunks                                           map[uint64][]byte
	readOffset, sendOffset, peerConsumed, advertised uint64
	buffered                                         int
	final                                            uint64
	hasFinal, writeClosed                            bool
	err                                              error
	readDeadline, writeDeadline                      time.Time
	bulkWriteCompleted                               time.Time // owned by writeMu
	receiveLimit, peerWindow                         int
	closeOnce                                        sync.Once
}
type closeState uint8

const (
	closedAbort closeState = iota + 1
	closedGraceful
)

func (s *Stream) StreamID() uint64 { return uint64(s.id) }

func newStream(session *Session, id uint32) *Stream {
	ctx, cancel := context.WithCancel(session.ctx)
	return &Stream{ctx: ctx, cancel: cancel, session: session, id: id, changed: make(chan struct{}), opened: make(chan struct{}), chunks: map[uint64][]byte{}, receiveLimit: initialStreamWindow, peerWindow: initialStreamWindow}
}
func (s *Stream) notifyLocked() { close(s.changed); s.changed = make(chan struct{}) }
func (s *Stream) markOpen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isOpen {
		s.isOpen = true
		close(s.opened)
	}
	s.notifyLocked()
}
func (s *Stream) abort(err error) {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
		s.session.receiveBuffered.Add(-int64(s.buffered))
		s.chunks = nil
		s.buffered = 0
		if !s.isOpen {
			s.isOpen = true
			close(s.opened)
		}
		s.notifyLocked()
	}
}
func (s *Stream) accept(f frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.kind == frameWindow || f.kind == frameReset {
		s.session.mu.Lock()
		terminal := s.session.closedStreams[s.id]
		if terminal != nil {
			err := s.session.closedFrameLocked(terminal, f)
			s.session.mu.Unlock()
			return err
		}
		s.session.mu.Unlock()
	}
	if s.err != nil && f.kind != frameWindow && f.kind != frameReset {
		return nil
	}
	switch f.kind {
	case frameOpenOK:
		if s.session.server || f.offset != 0 || len(f.data) != 0 {
			return ErrProtocol
		}
		if !s.isOpen {
			s.isOpen = true
			close(s.opened)
		}
	case frameData:
		if len(f.data) == 0 || f.offset > ^uint64(0)-uint64(len(f.data)) {
			return ErrProtocol
		}
		end := f.offset + uint64(len(f.data))
		if end <= s.readOffset {
			return nil
		}
		if end-s.readOffset > uint64(s.receiveLimit) || s.hasFinal && end > s.final {
			return ErrProtocol
		}
		if f.offset < s.readOffset {
			f.data = f.data[s.readOffset-f.offset:]
			f.offset = s.readOffset
		}
		if prior := s.chunks[f.offset]; prior != nil {
			if !bytes.Equal(prior, f.data) {
				return ErrProtocol
			}
			return nil
		}
		for at, b := range s.chunks {
			if f.offset < at+uint64(len(b)) && end > at {
				return ErrProtocol
			}
		}
		if s.buffered+len(f.data) > s.receiveLimit {
			return ErrCapacity
		}
		if !s.session.reserveReceive(len(f.data)) {
			return ErrCapacity
		}
		s.chunks[f.offset] = bytes.Clone(f.data)
		s.buffered += len(f.data)
	case frameWindow:
		if len(f.data) != 4 || f.offset > s.sendOffset {
			return ErrProtocol
		}
		window := int(binary.BigEndian.Uint32(f.data))
		if window < initialStreamWindow || window > streamWindow {
			return ErrProtocol
		}
		s.peerWindow = max(s.peerWindow, window)
		if err := s.consumedLocked(f.offset); err != nil {
			return err
		}
	case frameFIN:
		if len(f.data) != 0 || f.offset < s.readOffset || f.offset-s.readOffset > uint64(s.receiveLimit) || s.hasFinal && s.final != f.offset {
			return ErrProtocol
		}
		for at, b := range s.chunks {
			if at+uint64(len(b)) > f.offset {
				return ErrProtocol
			}
		}
		s.final, s.hasFinal = f.offset, true
	case frameReset:
		if f.offset != 0 || len(f.data) != 0 {
			return ErrProtocol
		}
		s.cancel()
		s.err = ErrClosed
		s.session.abandonStream(s.id)
		if err := s.consumedLocked(s.sendOffset); err != nil {
			return err
		}
		s.session.receiveBuffered.Add(-int64(s.buffered))
		s.chunks = nil
		s.buffered = 0
		if !s.isOpen {
			s.isOpen = true
			close(s.opened)
		}
	default:
		return ErrProtocol
	}
	s.notifyLocked()
	return nil
}

func waitStream(ctx context.Context, change, credit <-chan struct{}, deadline time.Time) error {
	var timer *time.Timer
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		if !deadline.After(time.Now()) {
			return os.ErrDeadlineExceeded
		}
		timer = time.NewTimer(time.Until(deadline))
		timeout = timer.C
		defer timer.Stop()
	}
	select {
	case <-ctx.Done():
		return ErrClosed
	case <-timeout:
		return os.ErrDeadlineExceeded
	case <-change:
		return nil
	case <-credit:
		return nil
	}
}
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		s.mu.Lock()
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return 0, err
		}
		if !s.readDeadline.IsZero() && !s.readDeadline.After(time.Now()) {
			s.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		if b := s.chunks[s.readOffset]; len(b) > 0 {
			n := copy(p, b)
			delete(s.chunks, s.readOffset)
			s.readOffset += uint64(n)
			s.buffered -= n
			s.session.receiveBuffered.Add(-int64(n))
			if n < len(b) {
				s.chunks[s.readOffset] = b[n:]
			}
			consumed := s.readOffset
			grew := false
			if consumed >= 64<<10 && s.receiveLimit == initialStreamWindow {
				s.receiveLimit = streamWindow
				grew = true
			}
			// The first window expansion remains immediate. Once bulk is
			// established, amortize credit announcements across 256 KiB.
			// Even 64 active streams leave at most 16 MiB unannounced,
			// below the shared 60 MiB bulk allowance. Close reports the tail.
			sendWindow := grew || consumed-s.advertised >= initialStreamWindow
			if sendWindow {
				s.advertised = consumed
			}
			window := s.receiveLimit
			s.mu.Unlock()
			if sendWindow {
				if err := s.session.send(s.session.ctx, frame{kind: frameWindow, stream: s.id, offset: consumed, data: binary.BigEndian.AppendUint32(nil, uint32(window))}, true); err != nil {
					return n, err
				}
			}
			return n, nil
		}
		if s.hasFinal && s.readOffset == s.final {
			s.mu.Unlock()
			return 0, io.EOF
		}
		change, deadline := s.changed, s.readDeadline
		s.mu.Unlock()
		if err := waitStream(s.session.ctx, change, nil, deadline); err != nil {
			return 0, err
		}
	}
}
func (s *Stream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		s.mu.Lock()
		if s.err != nil || s.writeClosed {
			err := s.err
			if err == nil {
				err = io.ErrClosedPipe
			}
			s.mu.Unlock()
			return written, err
		}
		if !s.writeDeadline.IsZero() && !s.writeDeadline.After(time.Now()) {
			s.mu.Unlock()
			return written, os.ErrDeadlineExceeded
		}
		room := uint64(s.peerWindow) - (s.sendOffset - s.peerConsumed)
		s.session.mu.Lock()
		credit := s.session.changed
		if s.sendOffset >= streamPrefix {
			room = min(room, bulkBudget-s.session.bulkOutstanding)
		} else {
			room = min(room, streamPrefix-s.sendOffset)
		}
		if room == 0 {
			change, deadline := s.changed, s.writeDeadline
			s.session.mu.Unlock()
			s.mu.Unlock()
			if err := waitStream(s.session.ctx, change, credit, deadline); err != nil {
				return written, err
			}
			continue
		}
		n := min(len(p), maxPayload-13, int(room))
		offset := s.sendOffset
		deadline := s.writeDeadline
		if offset > ^uint64(0)-uint64(n) {
			s.session.mu.Unlock()
			s.mu.Unlock()
			return written, ErrCapacity
		}
		// Reserve the offset before publication: a fast peer's WINDOW may
		// arrive before send returns. Write owns this reservation exclusively.
		s.sendOffset += uint64(n)
		s.session.bulkOutstanding += bulkBytes(s.sendOffset) - bulkBytes(offset)
		s.session.mu.Unlock()
		s.mu.Unlock()
		ctx := s.session.ctx
		cancel := func() {}
		if !deadline.IsZero() {
			ctx, cancel = context.WithDeadline(ctx, deadline)
		}
		// Tiny TLS/HTTP framing writes adjacent to a bulk write belong
		// to its DATA batch. Treating every trailer as interactive filled
		// the control writer with bulk framing. An isolated small write
		// still dispatches immediately; this test never waits or buffers.
		priority := n <= 4096 && time.Since(s.bulkWriteCompleted) > 2*time.Millisecond
		err := s.session.send(ctx, frame{kind: frameData, stream: s.id, offset: offset, data: p[:n]}, priority)
		cancel()
		if err != nil {
			s.mu.Lock()
			s.session.mu.Lock()
			if s.session.closedStreams[s.id] == nil {
				old := bulkBytes(s.sendOffset) - bulkBytes(s.peerConsumed)
				s.sendOffset = offset
				s.peerConsumed = min(s.peerConsumed, offset)
				remaining := bulkBytes(s.sendOffset) - bulkBytes(s.peerConsumed)
				s.session.bulkOutstanding -= old - remaining
				s.session.notifyLocked()
			}
			s.session.mu.Unlock()
			s.mu.Unlock()
			s.abort(err)
			return written, err
		}
		written += n
		if !priority {
			s.bulkWriteCompleted = time.Now()
		}
		p = p[n:]
	}
	return written, nil
}
func (s *Stream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	if s.writeClosed {
		s.mu.Unlock()
		return nil
	}
	s.writeClosed = true
	offset := s.sendOffset
	s.mu.Unlock()
	return s.session.send(s.session.ctx, frame{kind: frameFIN, stream: s.id, offset: offset}, true)
}
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		graceful := s.err == nil && s.writeClosed && s.hasFinal && s.readOffset == s.final
		peerReset := errors.Is(s.err, ErrClosed)
		consumed, window := s.readOffset, s.receiveLimit
		terminal := &terminalStream{state: closedAbort, sent: s.sendOffset, consumed: s.peerConsumed}
		if graceful {
			terminal.state = closedGraceful
		}
		if s.err == nil {
			s.err = ErrClosed
			s.session.receiveBuffered.Add(-int64(s.buffered))
			s.buffered = 0
			s.chunks = nil
			if !s.isOpen {
				s.isOpen = true
				close(s.opened)
			}
			s.notifyLocked()
		}
		s.session.mu.Lock()
		delete(s.session.streams, s.id)
		s.session.closedStreams[s.id] = terminal
		s.session.notifyLocked()
		s.session.mu.Unlock()
		s.mu.Unlock()
		if graceful {
			_ = s.session.send(s.session.ctx, frame{kind: frameWindow, stream: s.id, offset: consumed, data: binary.BigEndian.AppendUint32(nil, uint32(window))}, true)
		}
		if !graceful {
			s.session.abandonStream(s.id)
		}
		if !graceful && !peerReset {
			ctx, cancel := context.WithTimeout(s.session.ctx, time.Second)
			defer cancel()
			_ = s.session.send(ctx, frame{kind: frameReset, stream: s.id}, true)
		}
	})
	return nil
}

type streamAddr string

func (a streamAddr) Network() string   { return "mailstream" }
func (a streamAddr) String() string    { return string(a) }
func (s *Stream) LocalAddr() net.Addr  { return streamAddr("local") }
func (s *Stream) RemoteAddr() net.Addr { return streamAddr("peer") }
func (s *Stream) SetDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline, s.writeDeadline = t, t
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline = t
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.writeDeadline = t
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

func bridge(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyOne := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err == nil || errors.Is(err, io.EOF) {
			if closer, ok := dst.(interface{ CloseWrite() error }); ok {
				_ = closer.CloseWrite()
			}
		} else {
			a.Close()
			b.Close()
		}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	wg.Wait()
}
