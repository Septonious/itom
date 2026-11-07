package main

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// A pipelined propagation queue, not a sleep on each application Write. All
// sockets and both directions share this endpoint's finite serialization rate.
// This models application-visible RTT/bandwidth, not radio loss or kernel cwnd.
type network struct {
	mu                                    sync.Mutex
	next                                  time.Time
	start                                 time.Time
	readBitsPerSecond, writeBitsPerSecond int64
	readNext, writeNext                   time.Time
	bitsPerSecond                         int64
	dipStart time.Time
	dipDuration time.Duration
	dipBitsPerSecond int64
	rtts                                  []time.Duration
	step                                  time.Duration
	bytes                                 atomic.Uint64
	queued                                atomic.Int64
	peak                                  atomic.Int64
}

func (n *network) schedule(size int, outgoing bool) time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	rate, next := n.bitsPerSecond, &n.next
	if n.readBitsPerSecond > 0 && n.writeBitsPerSecond > 0 {
		rate, next = n.readBitsPerSecond, &n.readNext
		if outgoing {
			rate, next = n.writeBitsPerSecond, &n.writeNext
		}
	}
	begin := maxTime(now, *next)
	*next = n.serializationEnd(begin, int64(size)*8, rate)
	rtt := n.rtts[int(time.Since(n.start)/n.step)%len(n.rtts)]
	return next.Add(rtt / 2)
}
// Integrate finite link service across the dip boundaries, including bytes
// queued before the rate changes. The same queue is shared by every socket.
func (n *network) serializationEnd(begin time.Time, bits, baseRate int64) time.Time {
	for {
		rate := baseRate
		var boundary time.Time
		if n.dipBitsPerSecond > 0 {
			start := n.dipStart
			end := start.Add(n.dipDuration)
			if begin.Before(start) {
				boundary = start
			} else if begin.Before(end) {
				rate, boundary = n.dipBitsPerSecond, end
			}
		}
		needed := time.Duration((bits*int64(time.Second)+rate-1)/rate)
		if boundary.IsZero() || !begin.Add(needed).After(boundary) {
			return begin.Add(needed)
		}
		bits -= int64(boundary.Sub(begin))*rate/int64(time.Second)
		begin = boundary
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func (n *network) enqueued(size int) {
	queued := n.queued.Add(int64(size))
	for old := n.peak.Load(); queued > old && !n.peak.CompareAndSwap(old, queued); old = n.peak.Load() {
	}
}

type packet struct {
	body  []byte
	ready time.Time
	err   error
}
type delayedConn struct {
	net.Conn
	network         *network
	ctx             context.Context
	cancel          context.CancelFunc
	in, out         chan packet
	readMu, writeMu sync.Mutex
	mu              sync.Mutex
	rd, wd          time.Time
	changed         chan struct{}
	current         packet
	once            sync.Once
	pending         atomic.Int64
}

func shape(parent context.Context, conn net.Conn, network *network) *delayedConn {
	ctx, cancel := context.WithCancel(parent)
	c := &delayedConn{Conn: conn, network: network, ctx: ctx, cancel: cancel, in: make(chan packet, 32), out: make(chan packet, 32), changed: make(chan struct{})}
	go c.readPump()
	go c.writePump()
	return c
}
func (c *delayedConn) readPump() {
	defer close(c.in)
	for {
		body := make([]byte, 16<<10)
		n, err := c.Conn.Read(body)
		p := packet{err: err}
		if n > 0 {
			p.body = body[:n]
			p.ready = c.network.schedule(n, false)
			c.network.enqueued(n)
			c.network.bytes.Add(uint64(n))
		}
		select {
		case c.in <- p:
		case <-c.ctx.Done():
			if n > 0 {
				c.network.queued.Add(-int64(n))
			}
			return
		}
		if err != nil {
			return
		}
	}
}
func (c *delayedConn) writePump() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case p := <-c.out:
			if err := c.wait(p.ready, false); err != nil {
				c.cancel()
				c.Conn.Close()
				return
			}
			_, err := writeFull(c.Conn, p.body)
			c.network.queued.Add(-int64(len(p.body)))
			c.pending.Add(-int64(len(p.body)))
			if err != nil {
				c.cancel()
				c.Conn.Close()
				return
			}
			c.network.bytes.Add(uint64(len(p.body)))
		}
	}
}
func writeFull(w io.Writer, body []byte) (int, error) {
	total := 0
	for len(body) > 0 {
		n, err := w.Write(body)
		total += n
		body = body[n:]
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}
func (c *delayedConn) wait(ready time.Time, read bool) error {
	for {
		c.mu.Lock()
		deadline := c.wd
		if read {
			deadline = c.rd
		}
		changed := c.changed
		c.mu.Unlock()
		now := time.Now()
		if !deadline.IsZero() && !deadline.After(now) {
			return os.ErrDeadlineExceeded
		}
		if !ready.After(now) {
			return nil
		}
		end := ready
		if !deadline.IsZero() && deadline.Before(end) {
			end = deadline
		}
		timer := time.NewTimer(time.Until(end))
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return net.ErrClosed
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (c *delayedConn) Read(dst []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.current.body) == 0 && c.current.err == nil {
		select {
		case p, ok := <-c.in:
			if !ok {
				return 0, io.EOF
			}
			c.current = p
		case <-c.ctx.Done():
			return 0, net.ErrClosed
		}
	}
	if err := c.wait(c.current.ready, true); err != nil {
		return 0, err
	}
	n := copy(dst, c.current.body)
	c.current.body = c.current.body[n:]
	c.network.queued.Add(-int64(n))
	err := c.current.err
	if len(c.current.body) > 0 {
		err = nil
	}
	if n > 0 {
		return n, nil
	}
	c.current = packet{}
	return 0, err
}
func (c *delayedConn) Write(body []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	n := 0
	for len(body) > 0 {
		size := min(len(body), 16<<10)
		p := packet{body: append([]byte(nil), body[:size]...), ready: c.network.schedule(size, true)}
		c.network.enqueued(size)
		c.pending.Add(int64(size))
		select {
		case c.out <- p:
			n += size
			body = body[size:]
		case <-c.ctx.Done():
			c.network.queued.Add(-int64(size))
			return n, net.ErrClosed
		}
	}
	return n, nil
}
func (c *delayedConn) Close() error {
	c.once.Do(func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
	drain:
		for c.pending.Load() > 0 {
			select {
			case <-c.ctx.Done():
				break drain
			case <-deadline.C:
				break drain
			case <-tick.C:
			}
		}
		c.cancel()
		c.Conn.Close()
	})
	return nil
}
func (c *delayedConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.rd, c.wd = t, t
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}
func (c *delayedConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.rd = t
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	return c.Conn.SetReadDeadline(t)
}
func (c *delayedConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wd = t
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}

type shapedListener struct {
	net.Listener
	ctx     context.Context
	network *network
}

func (l shapedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return shape(l.ctx, conn, l.network), nil
}
