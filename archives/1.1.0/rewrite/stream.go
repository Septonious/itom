package mailstream

import (
	"bytes"
	"context"
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
}

func newStream(session *Session, id uint32) *Stream {
	return &Stream{session: session, id: id, changed: make(chan struct{}), opened: make(chan struct{}), chunks: map[uint64][]byte{}}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
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
	if s.err != nil {
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
		if f.offset < s.readOffset || end-s.readOffset > streamWindow || s.hasFinal && end > s.final {
			return ErrProtocol
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
		if s.buffered+len(f.data) > streamWindow {
			return ErrCapacity
		}
		s.chunks[f.offset] = bytes.Clone(f.data)
		s.buffered += len(f.data)
	case frameWindow:
		if len(f.data) != 0 || f.offset > s.sendOffset {
			return ErrProtocol
		}
		s.peerConsumed = max(s.peerConsumed, f.offset)
	case frameFIN:
		if len(f.data) != 0 || f.offset < s.readOffset || f.offset-s.readOffset > streamWindow || s.hasFinal && s.final != f.offset {
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
		s.err = ErrClosed
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

func waitStream(ctx context.Context, change <-chan struct{}, deadline time.Time) error {
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
			if n < len(b) {
				s.chunks[s.readOffset] = b[n:]
			}
			consumed := s.readOffset
			sendWindow := consumed-s.advertised >= streamWindow/4
			if sendWindow {
				s.advertised = consumed
			}
			s.mu.Unlock()
			if sendWindow {
				if err := s.session.send(s.session.ctx, frame{kind: frameWindow, stream: s.id, offset: consumed}, true); err != nil {
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
		if err := waitStream(s.session.ctx, change, deadline); err != nil {
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
		room := uint64(streamWindow) - (s.sendOffset - s.peerConsumed)
		if room == 0 {
			change, deadline := s.changed, s.writeDeadline
			s.mu.Unlock()
			if err := waitStream(s.session.ctx, change, deadline); err != nil {
				return written, err
			}
			continue
		}
		n := min(len(p), maxPayload-13, int(room))
		offset := s.sendOffset
		deadline := s.writeDeadline
		if offset > ^uint64(0)-uint64(n) {
			s.mu.Unlock()
			return written, ErrCapacity
		}
		// Reserve the offset before publication: a fast peer's WINDOW may
		// arrive before send returns. Write owns this reservation exclusively.
		s.sendOffset += uint64(n)
		s.mu.Unlock()
		ctx := s.session.ctx
		cancel := func() {}
		if !deadline.IsZero() {
			ctx, cancel = context.WithDeadline(ctx, deadline)
		}
		err := s.session.send(ctx, frame{kind: frameData, stream: s.id, offset: offset, data: p[:n]}, n <= 4096)
		cancel()
		if err != nil {
			s.abort(err)
			return written, err
		}
		written += n
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
	s.abort(ErrClosed)
	s.session.mu.Lock()
	delete(s.session.streams, s.id)
	s.session.mu.Unlock()
	// A reset is best effort when the user already canceled the session.
	ctx, cancel := context.WithTimeout(s.session.ctx, time.Second)
	defer cancel()
	_ = s.session.send(ctx, frame{kind: frameReset, stream: s.id}, true)
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
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		if err != nil && !errors.Is(err, io.EOF) {
			a.Close()
			b.Close()
		}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	wg.Wait()
}
