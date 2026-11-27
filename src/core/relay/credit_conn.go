package relay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// The TLS connection's TCP buffers alone cannot carry application backpressure
// through a buffering intermediary. The optional ALPN v2 protocol grants
// bounded, per-direction, consumed-byte credit. FIN is independent of WINDOW,
// so a half-closed sender can still return credit for its incoming stream.
const (
	relayProtocolV1           = "olcrtc-relay/1"
	relayProtocolV2           = "olcrtc-relay/2"
	relayCreditWindow         = 512 << 10
	relayFrameBytes           = 32 << 10
	relayWindowThreshold      = 32 << 10
	relayData            byte = 1
	relayWindow          byte = 2
	relayFIN             byte = 3
)

var errRelayCredit = errors.New("invalid relay credit frame")

type creditConn struct {
	net.Conn
	mu                          sync.Mutex
	changed                     chan struct{}
	sendCredit, recvCredit      int
	unannounced                 int
	buffer                      bytes.Buffer
	peerFIN, localFIN           bool
	terminal                    error
	readDeadline, writeDeadline time.Time
	readMu, writeMu, txMu       sync.Mutex
}

func newCreditConn(conn net.Conn) *creditConn {
	c := &creditConn{Conn: conn, changed: make(chan struct{}), sendCredit: relayCreditWindow, recvCredit: relayCreditWindow}
	go c.receive()
	return c
}
func (c *creditConn) notifyLocked() { close(c.changed); c.changed = make(chan struct{}) }
func (c *creditConn) fail(err error) {
	c.mu.Lock()
	if c.terminal == nil {
		c.terminal = err
		c.notifyLocked()
	}
	c.mu.Unlock()
	_ = c.Conn.Close()
}
func (c *creditConn) Close() error { c.fail(net.ErrClosed); return nil }

func (c *creditConn) receive() {
	var h [5]byte
	var payload [relayFrameBytes]byte
	for {
		if _, err := io.ReadFull(c.Conn, h[:]); err != nil {
			c.fail(err)
			return
		}
		kind, n := h[0], int(binary.BigEndian.Uint32(h[1:]))
		switch kind {
		case relayData:
			c.mu.Lock()
			if n <= 0 || n > relayFrameBytes || n > c.recvCredit || c.peerFIN {
				c.mu.Unlock()
				c.fail(errRelayCredit)
				return
			}
			c.recvCredit -= n
			c.mu.Unlock()
			b := payload[:n]
			if _, err := io.ReadFull(c.Conn, b); err != nil {
				c.fail(err)
				return
			}
			c.mu.Lock()
			_, _ = c.buffer.Write(b)
			c.notifyLocked()
			c.mu.Unlock()
		case relayWindow:
			c.mu.Lock()
			if n <= 0 || n > relayCreditWindow-c.sendCredit {
				c.mu.Unlock()
				c.fail(errRelayCredit)
				return
			}
			c.sendCredit += n
			c.notifyLocked()
			c.mu.Unlock()
		case relayFIN:
			c.mu.Lock()
			if n != 0 || c.peerFIN {
				c.mu.Unlock()
				c.fail(errRelayCredit)
				return
			}
			c.peerFIN = true
			c.notifyLocked()
			c.mu.Unlock()
		default:
			c.fail(errRelayCredit)
			return
		}
	}
}
func creditWait(ch <-chan struct{}, deadline time.Time) error {
	if deadline.IsZero() {
		<-ch
		return nil
	}
	if !time.Now().Before(deadline) {
		return os.ErrDeadlineExceeded
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-timer.C:
		return os.ErrDeadlineExceeded
	}
}
func (c *creditConn) sendFrame(kind byte, n int, data []byte) error {
	c.txMu.Lock()
	defer c.txMu.Unlock()
	var h [5]byte
	h[0] = kind
	binary.BigEndian.PutUint32(h[1:], uint32(n))
	// One TLS Write keeps the frame header and a small payload together.
	frame := h[:]
	if len(data) > 0 {
		frame = make([]byte, 5+len(data))
		copy(frame, h[:])
		copy(frame[5:], data)
	}
	written, err := c.Conn.Write(frame)
	if err == nil && written != len(frame) {
		err = io.ErrShortWrite
	}
	if err != nil {
		c.fail(err)
	}
	return err
}
func (c *creditConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		c.mu.Lock()
		if !c.readDeadline.IsZero() && !time.Now().Before(c.readDeadline) {
			c.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		if c.buffer.Len() > 0 {
			n, _ := c.buffer.Read(p)
			c.unannounced += n
			grant := 0
			// A receiver with no queued bytes must never strand the last credit.
			if c.unannounced >= relayWindowThreshold || c.buffer.Len() == 0 {
				grant = c.unannounced
				c.unannounced = 0
				c.recvCredit += grant
			}
			terminal, finished := c.terminal, c.peerFIN
			c.mu.Unlock()
			if grant > 0 && terminal == nil && !finished {
				if err := c.sendFrame(relayWindow, grant, nil); err != nil {
					return n, err
				}
			}
			return n, nil
		}
		if c.peerFIN {
			c.mu.Unlock()
			return 0, io.EOF
		}
		if c.terminal != nil {
			err := c.terminal
			c.mu.Unlock()
			return 0, err
		}
		ch, deadline := c.changed, c.readDeadline
		c.mu.Unlock()
		if err := creditWait(ch, deadline); err != nil {
			return 0, err
		}
	}
}
func (c *creditConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		c.mu.Lock()
		if c.terminal != nil {
			err := c.terminal
			c.mu.Unlock()
			return total, err
		}
		if c.localFIN {
			c.mu.Unlock()
			return total, io.ErrClosedPipe
		}
		if !c.writeDeadline.IsZero() && !time.Now().Before(c.writeDeadline) {
			c.mu.Unlock()
			return total, os.ErrDeadlineExceeded
		}
		n := min(len(p), relayFrameBytes, c.sendCredit)
		if n == 0 {
			ch, deadline := c.changed, c.writeDeadline
			c.mu.Unlock()
			if err := creditWait(ch, deadline); err != nil {
				return total, err
			}
			continue
		}
		c.sendCredit -= n
		c.mu.Unlock()
		if err := c.sendFrame(relayData, n, p[:n]); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}
func (c *creditConn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if c.localFIN {
		c.mu.Unlock()
		return nil
	}
	if c.terminal != nil {
		err := c.terminal
		c.mu.Unlock()
		return err
	}
	c.localFIN = true
	c.mu.Unlock()
	return c.sendFrame(relayFIN, 0, nil)
}
func (c *creditConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.notifyLocked()
	c.mu.Unlock()
	return nil
}
func (c *creditConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.notifyLocked()
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}
func (c *creditConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}
