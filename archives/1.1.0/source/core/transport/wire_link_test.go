package transport_test

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// This test-only route delays opaque wire bytes, not protocol commands. Queues
// are bounded, both directions propagate independently and all connections on
// an endpoint share each direction's serialization budget. It is a user-space
// TCP link model, not kernel netem, packet loss simulation or a field result.
type wireRoute struct {
	rtts           []time.Duration
	phase          time.Duration
	bytesPerSecond uint64
	started        time.Time
	up, down       wireDirection
	buffers        sync.Pool
}
type wireDirection struct {
	mu   sync.Mutex
	next time.Time
}
type wirePacket struct {
	bytes []byte
	at    time.Time
}

const wirePacketBytes = 16 << 10
const wireQueuePackets = 32

func newWireRoute(rtts []time.Duration, phase time.Duration, bytesPerSecond uint64) *wireRoute {
	if len(rtts) == 0 || phase <= 0 || bytesPerSecond == 0 {
		panic("invalid explicit test wire profile")
	}
	for _, rtt := range rtts {
		if rtt <= 0 {
			panic("invalid test wire RTT")
		}
	}
	r := &wireRoute{rtts: append([]time.Duration(nil), rtts...), phase: phase, bytesPerSecond: bytesPerSecond, started: time.Now()}
	r.buffers.New = func() any { return make([]byte, wirePacketBytes) }
	return r
}
func (r *wireRoute) schedule(d *wireDirection, n int) time.Time {
	now := time.Now()
	delay := r.rtts[int(now.Sub(r.started)/r.phase)%len(r.rtts)] / 2
	d.mu.Lock()
	defer d.mu.Unlock()
	start := now
	if d.next.After(start) {
		start = d.next
	}
	transmission := time.Duration((uint64(n)*uint64(time.Second) + r.bytesPerSecond - 1) / r.bytesPerSecond)
	d.next = start.Add(transmission)
	return d.next.Add(delay)
}

type wireConn struct {
	net.Conn
	shutdown func()
	workers  *sync.WaitGroup
}

func (c *wireConn) Close() error      { err := c.Conn.Close(); c.shutdown(); c.workers.Wait(); return err }
func (c *wireConn) CloseWrite() error { return c.Conn.(*net.TCPConn).CloseWrite() }

func (r *wireRoute) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Real OS TCP on both sides of the relay; TLS remains end to end between
	// DialSelected and its provider. Dial cancellation does not own stream life.
	target, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		target.Close()
		return nil, err
	}
	client, err := (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		listener.Close()
		target.Close()
		return nil, err
	}
	relay, err := listener.Accept()
	listener.Close()
	if err != nil {
		client.Close()
		target.Close()
		return nil, err
	}
	done := make(chan struct{})
	var once sync.Once
	shutdown := func() { once.Do(func() { close(done); client.Close(); relay.Close(); target.Close() }) }
	workers := &sync.WaitGroup{}
	run := func(source, destination net.Conn, direction *wireDirection) {
		packets := make(chan wirePacket, wireQueuePackets)
		workers.Go(func() {
			defer close(packets)
			for {
				buffer := r.buffers.Get().([]byte)
				n, readErr := source.Read(buffer)
				if n > 0 {
					packet := wirePacket{bytes: buffer[:n], at: r.schedule(direction, n)}
					select {
					case packets <- packet:
					case <-done:
						r.buffers.Put(buffer)
						return
					}
				} else {
					r.buffers.Put(buffer)
				}
				if readErr != nil {
					if readErr != io.EOF {
						shutdown()
					}
					return
				}
			}
		})
		workers.Go(func() {
			defer func() {
				for packet := range packets {
					r.buffers.Put(packet.bytes[:wirePacketBytes])
				}
			}()
			timer := time.NewTimer(time.Hour)
			timer.Stop()
			defer timer.Stop()
			for {
				select {
				case <-done:
					return
				case packet, ok := <-packets:
					if !ok {
						if half, ok := destination.(interface{ CloseWrite() error }); ok {
							half.CloseWrite()
						}
						return
					}
					if wait := time.Until(packet.at); wait > 0 {
						timer.Reset(wait)
						select {
						case <-done:
							r.buffers.Put(packet.bytes[:wirePacketBytes])
							return
						case <-timer.C:
						}
					}
					remaining := packet.bytes
					var writeErr error
					for len(remaining) > 0 {
						n, err := destination.Write(remaining)
						remaining = remaining[n:]
						if err != nil {
							writeErr = err
							break
						}
						if n == 0 {
							writeErr = io.ErrShortWrite
							break
						}
					}
					r.buffers.Put(packet.bytes[:wirePacketBytes])
					if writeErr != nil {
						shutdown()
						return
					}
				}
			}
		})
	}
	run(relay, target, &r.up)
	run(target, relay, &r.down)
	go func() { workers.Wait(); relay.Close(); target.Close() }()
	return &wireConn{Conn: client, shutdown: shutdown, workers: workers}, nil
}
