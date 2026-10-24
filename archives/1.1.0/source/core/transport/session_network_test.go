package transport_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This is application/TCP integration, not primary provider performance:
// fast and bulk have distinct OS TCP sockets and production framing/Noise,
// but no IMAP or quota I/O. No rate or latency here qualifies a release.
func tcpCarrierPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	failed := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			failed <- e
			return
		}
		accepted <- conn
	}()
	local, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	select {
	case peer := <-accepted:
		listener.Close()
		return local, peer
	case err := <-failed:
		local.Close()
		listener.Close()
		t.Fatal(err)
	}
	return nil, nil
}
func logicalLink(t *testing.T) (*transport.Session, *transport.Session, context.Context) {
	t.Helper()
	a := session(t, transport.DefaultLimits())
	b := session(t, transport.DefaultLimits())
	ca, cb := cipherPair(t, wire.ID{88})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var sockets []net.Conn
	fail := func(err error) {
		if ctx.Err() == nil {
			t.Errorf("logical TCP carrier: %v", err)
			cancel()
		}
	}
	maximum, err := secure.SessionPacketSize(secure.SessionMaxPlaintext)
	if err != nil {
		t.Fatal(err)
	}
	for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
		left, right := tcpCarrierPair(t)
		sockets = append(sockets, left, right)
		for _, side := range []struct {
			source, destination *transport.Session
			sender, receiver    *transport.Cipher
			socket              net.Conn
		}{{a, a, ca, ca, left}, {b, b, cb, cb, right}} {
			wg.Go(func() {
				stop := context.AfterFunc(ctx, func() { side.socket.Close() })
				defer stop()
				for {
					wake := side.source.WorkChanged()
					dispatch, ok := side.source.Take(plane, transport.MaxPayload)
					if !ok {
						select {
						case <-ctx.Done():
							return
						case <-wake:
						}
						continue
					}
					packet, err := side.sender.Seal(dispatch.Record)
					if err != nil {
						fail(err)
						return
					}
					framed := binary.BigEndian.AppendUint32(nil, uint32(len(packet)))
					framed = append(framed, packet...)
					for len(framed) > 0 {
						n, e := side.socket.Write(framed)
						if e != nil {
							fail(e)
							return
						}
						if n == 0 {
							fail(io.ErrNoProgress)
							return
						}
						framed = framed[n:]
					}
					if err = dispatch.Complete(transport.Accepted); err != nil && !errors.Is(err, transport.ErrClosed) {
						fail(err)
						return
					}
				}
			})
			wg.Go(func() {
				stop := context.AfterFunc(ctx, func() { side.socket.Close() })
				defer stop()
				for {
					var prefix [4]byte
					if _, e := io.ReadFull(side.socket, prefix[:]); e != nil {
						fail(e)
						return
					}
					n := binary.BigEndian.Uint32(prefix[:])
					if n == 0 || n > uint32(maximum) {
						fail(transport.ErrProtocol)
						return
					}
					packet := make([]byte, int(n))
					if _, e := io.ReadFull(side.socket, packet); e != nil {
						fail(e)
						return
					}
					record, e := side.receiver.Open(packet)
					if e == nil {
						e = side.destination.Receive(record)
					}
					if e != nil && !errors.Is(e, transport.ErrClosed) {
						fail(e)
						return
					}
				}
			})
		}
	}
	t.Cleanup(func() {
		cancel()
		for _, conn := range sockets {
			conn.Close()
		}
		a.Close()
		b.Close()
		wg.Wait()
	})
	return a, b, ctx
}
func TestRepeatedColdTLSHTTPRequestsReuseOneSessionAndRetireAllStreams(t *testing.T) {
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		io.WriteString(w, strings.Repeat("verified", 8192))
	}))
	upstream.Config.ErrorLog = log.New(io.Discard, "", 0)
	upstream.StartTLS()
	t.Cleanup(upstream.Close)
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	address := strings.TrimPrefix(upstream.URL, "https://")
	a, b, ctx := logicalLink(t)
	var sequence atomic.Uint64
	var bridges sync.WaitGroup
	transportHTTP := &http.Transport{DisableKeepAlives: true, DialTLSContext: func(open context.Context, network, target string) (net.Conn, error) {
		id := sequence.Add(2) - 1
		if err := a.Add(id, 1<<20, 1<<20); err != nil {
			return nil, err
		}
		if err := b.Add(id, 1<<20, 1<<20); err != nil {
			return nil, err
		}
		local, err := a.Conn(id)
		if err != nil {
			return nil, err
		}
		remote, err := b.Conn(id)
		if err != nil {
			return nil, err
		}
		targetConn, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			return nil, err
		}
		socket := targetConn.(*net.TCPConn)
		bridges.Go(func() {
			defer remote.Close()
			defer socket.Close()
			stop := context.AfterFunc(ctx, func() { socket.Close() })
			defer stop()
			done := make(chan struct{})
			go func() { io.Copy(socket, remote); socket.CloseWrite(); close(done) }()
			io.Copy(remote, socket)
			remote.CloseWrite()
			<-done
		})
		host, _, _ := net.SplitHostPort(address)
		encrypted := tls.Client(local, &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12})
		if err = encrypted.HandshakeContext(open); err != nil {
			local.Close()
			return nil, err
		}
		return encrypted, nil
	}}
	client := &http.Client{Transport: transportHTTP, Timeout: 3 * time.Second}
	for range 20 {
		response, err := client.Get(upstream.URL)
		if err != nil {
			id := sequence.Load() - 1
			left, _ := a.FlowSnapshot(id)
			right, _ := b.FlowSnapshot(id)
			t.Fatal("cold TLS/HTTP failed", err, "client", left, "exit", right)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != strings.Repeat("verified", 8192) {
			t.Fatal("TLS response lost bytes", err)
		}
	}
	transportHTTP.CloseIdleConnections()
	bridges.Wait()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		changedA, changedB := a.WorkChanged(), b.WorkChanged()
		sa, sb := a.Snapshot(), b.Snapshot()
		if sa.Streams == 0 && sb.Streams == 0 {
			if sa.SendBuffered != 0 || sb.SendBuffered != 0 || sa.ReceiveReserved != 0 || sb.ReceiveReserved != 0 {
				t.Fatal("retired session retained memory", sa, sb)
			}
			break
		}
		select {
		case <-changedA:
		case <-changedB:
		case <-deadline.C:
			t.Fatal("finished TLS flows leaked session slots", sa, sb)
		}
	}
	if sequence.Load() != 40 {
		t.Fatal("test did not create twenty distinct cold connections")
	}
}
func TestFullDuplexApplicationBytesAndFINAcrossIndependentPlanes(t *testing.T) {
	a, b, _ := logicalLink(t)
	add(t, a, 1, 256<<10, 256<<10)
	add(t, b, 1, 256<<10, 256<<10)
	ac, err := a.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	bc, err := b.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	payloadA := strings.Repeat("u", 2<<20)
	payloadB := strings.Repeat("d", 3<<20)
	errors := make(chan error, 2)
	go func() {
		_, e := io.WriteString(ac, payloadA)
		if e == nil {
			e = ac.CloseWrite()
		}
		errors <- e
	}()
	go func() {
		_, e := io.WriteString(bc, payloadB)
		if e == nil {
			e = bc.CloseWrite()
		}
		errors <- e
	}()
	// Byte-integrity fixture: bound absence of useful progress. Fixed-time
	// throughput, ramp, tail and RTT gates live in qualification, not this test.
	readerA := &progressReader{conn: ac, last: time.Now(), limit: 5 * time.Second}
	readerB := &progressReader{conn: bc, last: time.Now(), limit: 5 * time.Second}
	received := make(chan string, 1)
	go func() {
		out, e := io.ReadAll(readerB)
		if e != nil {
			t.Error(e)
		}
		received <- string(out)
	}()
	out, e := io.ReadAll(readerA)
	if e != nil || string(out) != payloadB {
		sa, _ := a.FlowSnapshot(1)
		sb, _ := b.FlowSnapshot(1)
		t.Fatal("download corrupted", len(out), e, "sender", sa, "receiver", sb)
	}
	if got := <-received; got != payloadA {
		t.Fatal("upload corrupted", len(got))
	}
	for range 2 {
		if e := <-errors; e != nil {
			t.Fatal(e)
		}
	}
	ac.Close()
	bc.Close()
}

// The progress bound is measured on the application's local monotonic clock;
// neither an empty read nor an internal control exchange resets it.
type progressReader struct {
	conn  net.Conn
	last  time.Time
	limit time.Duration
}

func (r *progressReader) Read(b []byte) (int, error) {
	r.conn.SetReadDeadline(r.last.Add(r.limit))
	n, err := r.conn.Read(b)
	if n > 0 {
		r.last = time.Now()
	}
	return n, err
}
