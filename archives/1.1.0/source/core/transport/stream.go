package transport

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// Conn is the application-facing stream after authenticated OPEN_OK. Its
// lifetime is independent of the context used to establish OPEN, and of every
// physical carrier. Closing it resets this stream without touching a provider.
type Conn struct {
	session                     *Session
	id                          uint64
	readMu, writeMu             sync.Mutex
	deadlineMu                  sync.Mutex
	readDeadline, writeDeadline time.Time
	deadlineChanged             chan struct{}
}

var _ net.Conn = (*Conn)(nil)

func (s *Session) Conn(id uint64) (*Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if s.closed || f == nil || f.stopped() {
		return nil, ErrClosed
	}
	if f.connection == nil {
		f.connection = &Conn{session: s, id: id, deadlineChanged: make(chan struct{})}
	}
	return f.connection, nil
}
func (c *Conn) ID() uint64 { return c.id }
func (c *Conn) deadline(read bool) (time.Time, <-chan struct{}) {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if read {
		return c.readDeadline, c.deadlineChanged
	}
	return c.writeDeadline, c.deadlineChanged
}
func (c *Conn) wait(read bool, changed, space <-chan struct{}) error {
	deadline, updated := c.deadline(read)
	var timer *time.Timer
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return os.ErrDeadlineExceeded
		}
		timer = time.NewTimer(remaining)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-changed:
		return nil
	case <-space:
		return nil
	case <-updated:
		return nil
	case <-timeout:
		return os.ErrDeadlineExceeded
	}
}
func (c *Conn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	for {
		deadline, _ := c.deadline(true)
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		s := c.session
		s.mu.Lock()
		f := s.flows[c.id]
		if f == nil || s.closed {
			s.mu.Unlock()
			return 0, ErrClosed
		}
		changed := f.notify
		n, err := s.readLocked(c.id, b)
		s.mu.Unlock()
		if n != 0 || err != nil {
			return n, err
		}
		if err = c.wait(true, changed, nil); err != nil {
			return 0, err
		}
	}
}
func (c *Conn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for written < len(b) {
		deadline, _ := c.deadline(false)
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return written, os.ErrDeadlineExceeded
		}
		s := c.session
		s.mu.Lock()
		f := s.flows[c.id]
		if s.closed || f == nil || f.stopped() || f.localFinish || f.applicationClosed {
			s.mu.Unlock()
			return written, ErrClosed
		}
		room := s.limits.StreamBytes - f.buffered
		if remaining := s.limits.SessionBytes - s.buffered; room > remaining {
			room = remaining
		}
		n := len(b) - written
		if uint64(n) > room {
			n = int(room)
		}
		if n > MaxPayload {
			n = MaxPayload
		}
		changed, space := f.notify, s.space
		if n > 0 {
			err := s.queueLocked(c.id, b[written:written+n], time.Now())
			s.mu.Unlock()
			if err != nil {
				return written, err
			}
			written += n
			continue
		}
		s.mu.Unlock()
		if err := c.wait(false, changed, space); err != nil {
			return written, err
		}
	}
	return written, nil
}
func (c *Conn) CloseWrite() error {
	// Serialize with Write, so FIN cannot pass a partially admitted application
	// write. Closing the whole stream does not wait on this lock.
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.session.Finish(c.id)
}
func (c *Conn) Close() error {
	s := c.session
	s.mu.Lock()
	f := s.flows[c.id]
	if f == nil || s.closed || f.applicationClosed {
		s.mu.Unlock()
		return nil
	}
	f.applicationClosed = true
	f.signal()
	if f.stopped() {
		_ = s.retireLocked(c.id)
		s.mu.Unlock()
		return nil
	}
	graceful := f.localFinish && f.remoteFinish && f.consumed == f.final
	if graceful {
		_ = s.retireLocked(c.id)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	err := s.abort(c.id)
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}
func (c *Conn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.deadlineMu.Unlock()
	return nil
}
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.deadlineMu.Unlock()
	return nil
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.writeDeadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.deadlineMu.Unlock()
	return nil
}

type streamAddr struct {
	id   uint64
	peer bool
}

func (streamAddr) Network() string { return "itom-session-v2" }
func (a streamAddr) String() string {
	if a.peer {
		return "peer"
	}
	return "local"
}
func (c *Conn) LocalAddr() net.Addr  { return streamAddr{id: c.id} }
func (c *Conn) RemoteAddr() net.Addr { return streamAddr{id: c.id, peer: true} }
