package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"itom.local/core/transport"
)

// A private virtual exit is reachable only through the authenticated session.
// Its ordinary OPEN/DATA/receipt publications use the original bounded wallets
// and journals. No public socket, raw IMAP timing, or synthetic sample is used.
const probeHost = "rtt.itom.invalid"
const probePort = 1
const probeBytes = 16

type ProbeSnapshot struct {
	Samples, Failures      uint64
	RTTMillis, SampledAtMS int64
	Failure                string
}

func echoExit(target transport.Target) (net.Conn, bool, error) {
	if !strings.EqualFold(target.Host, probeHost) {
		return nil, false, nil
	}
	if target.Port != probePort {
		return nil, true, transport.OpenDenied
	}
	local, peer := net.Pipe()
	go func() {
		defer peer.Close()
		var b [probeBytes]byte
		for {
			if _, err := io.ReadFull(peer, b[:]); err != nil {
				return
			}
			if _, err := peer.Write(b[:]); err != nil {
				return
			}
		}
	}()
	return local, true, nil
}

func echoSample(ctx context.Context, conn net.Conn) (time.Duration, error) {
	var nonce, reply [probeBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	// The probe has its own existing-OPEN-sized ceiling, never a larger
	// application or IMAP deadline. Only one sample can be in flight.
	if err := conn.SetDeadline(time.Now().Add(1500 * time.Millisecond)); err != nil {
		return 0, err
	}
	begin := time.Now()
	if n, err := conn.Write(nonce[:]); err != nil {
		return 0, err
	} else if n != len(nonce) {
		return 0, io.ErrShortWrite
	}
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return 0, err
	}
	if !bytes.Equal(nonce[:], reply[:]) {
		return 0, transport.ErrProtocol
	}
	return time.Since(begin), nil
}

func (c *Client) runProbe(ctx context.Context) {
	defer c.workers.Done()
	var conn net.Conn
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	for ctx.Err() == nil {
		var err error
		if conn == nil {
			conn, err = c.endpoint.Service.Dialer.DialContext(ctx, "tcp", net.JoinHostPort(probeHost, "1"))
		}
		var elapsed time.Duration
		if err == nil {
			elapsed, err = echoSample(ctx, conn)
		}
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		if err == nil {
			c.probe.Samples++
			c.probe.RTTMillis = max(int64(1), elapsed.Milliseconds())
			c.probe.SampledAtMS = time.Now().UnixMilli()
			c.probe.Failure = ""
		} else {
			c.probe.Failures++
			c.probe.RTTMillis = 0
			c.probe.Failure = "PROBE_FAILED"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
				c.probe.Failure = "PROBE_TIMEOUT"
			}
		}
		c.mu.Unlock()
		if err != nil && conn != nil {
			conn.Close()
			conn = nil
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-c.endpoint.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
