package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestRTTRequiresExactPeerEchoAndIncludesRealRoundTripWait(t *testing.T) {
	for _, mode := range []string{"echo", "wrong_nonce", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer peer.Close()
				var b [probeBytes]byte
				if _, err := io.ReadFull(peer, b[:]); err != nil {
					return
				}
				time.Sleep(25 * time.Millisecond)
				if mode == "wrong_nonce" {
					b[0] ^= 1
				}
				_, _ = peer.Write(b[:])
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			elapsed, err := echoSample(ctx, local)
			switch mode {
			case "echo":
				if err != nil || elapsed < 25*time.Millisecond {
					t.Fatal("missing real echo wait", elapsed, err)
				}
			case "wrong_nonce":
				if !errors.Is(err, transport.ErrProtocol) || elapsed != 0 {
					t.Fatal("invented RTT from wrong peer nonce", elapsed, err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) || elapsed != 0 {
					t.Fatal("invented canceled RTT", elapsed, err)
				}
			}
			local.Close()
			<-done
		})
	}
}

func TestVirtualProbeDoesNotHandleOrdinaryDestinationsOrWrongPort(t *testing.T) {
	for _, host := range []string{"example.com", "203.0.113.80", "rtt.itom.invalid.evil"} {
		conn, handled, err := echoExit(transport.Target{Host: host, Port: 443})
		if conn != nil || handled || err != nil {
			t.Fatal("ordinary exit intercepted", host)
		}
	}
	conn, handled, err := echoExit(transport.Target{Host: probeHost, Port: 443})
	if conn != nil || !handled || !errors.Is(err, transport.OpenDenied) {
		t.Fatal("wrong virtual port admitted", conn, handled, err)
	}
	conn, handled, err = echoExit(transport.Target{Host: probeHost, Port: probePort})
	if conn == nil || !handled || err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if elapsed, err := echoSample(context.Background(), conn); err != nil || elapsed <= 0 {
		t.Fatal(elapsed, err)
	}
}

func TestProbeCancellationUnblocksReadWithoutClosingTheCarrier(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reading := make(chan struct{})
	go func() { var b [probeBytes]byte; io.ReadFull(peer, b[:]); close(reading); cancel() }()
	begin := time.Now()
	elapsed, err := echoSample(ctx, local)
	if err == nil || elapsed != 0 || time.Since(begin) > time.Second {
		t.Fatal("canceled sample stayed blocked", elapsed, err)
	}
	<-reading
}
