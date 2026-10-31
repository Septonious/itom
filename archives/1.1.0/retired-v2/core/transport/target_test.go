package transport_test

import (
	"context"
	"errors"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestTCPTargetCanonicalizationRejectsAmbiguousNamesAndZones(t *testing.T) {
	for input, expected := range map[string]string{"EXAMPLE.com:443": "example.com", "[2001:db8::1]:443": "2001:db8::1", "[::ffff:192.0.2.1]:80": "192.0.2.1"} {
		target, err := transport.ParseTarget(input)
		if err != nil || target.Host != expected {
			t.Fatal("target changed", input, target, err)
		}
	}
	for _, invalid := range []string{"example.com:0", "example.com:65536", "example.com:https", "user@example.com:443", "https://example.com:443", "[fe80::1%eth0]:443", "example..com:443", "-bad.example:443", "bad.example.:443", "example.com\r\n:443"} {
		if _, err := transport.ParseTarget(invalid); err == nil {
			t.Fatal("ambiguous target accepted", invalid)
		}
	}
}

func TestVariableOpenPayloadCannotBypassGrantQuantumOrDataLedger(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	dialer, err := transport.NewClientDialer(s, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := dialer.DialContext(ctx, "tcp", "example.test:443")
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	wake := s.WorkChanged()
	if !s.ReadyFor(transport.Fast, transport.ControlLedger) {
		select {
		case <-wake:
		case <-time.After(time.Second):
			t.Fatal("OPEN not queued")
		}
	}
	if _, ok := s.TakeFor(transport.Fast, 0, transport.ControlLedger); ok {
		t.Fatal("OPEN exceeded a zero-payload physical ticket")
	}
	if _, ok := s.TakeFor(transport.Fast, transport.MaxPayload, transport.DataLedger); ok {
		t.Fatal("OPEN borrowed DATA authority")
	}
	request, ok := s.TakeFor(transport.Fast, transport.MaxPayload, transport.ControlLedger)
	if !ok || request.Record.Kind != transport.Open || len(request.Record.Payload) == 0 {
		t.Fatal("OPEN disappeared after rejected admission")
	}
	complete(t, request, transport.NotStarted)
	if _, ok := s.TakeFor(transport.Fast, 0, transport.ControlLedger); ok {
		t.Fatal("unstarted OPEN retry exceeded physical ticket")
	}
	retry, ok := s.TakeFor(transport.Fast, transport.MaxPayload, transport.ControlLedger)
	if !ok || retry.Record.Kind != transport.Open || string(retry.Record.Payload) != string(request.Record.Payload) {
		t.Fatal("unstarted OPEN retry lost immutable target")
	}
	complete(t, retry, transport.Uncertain)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reset := take(t, s, transport.Fast, 0)
	if reset.Record.Kind != transport.Reset {
		t.Fatal("cancellation did not reset uncertain OPEN")
	}
	complete(t, reset, transport.Accepted)
	if err := s.Receive(incoming(transport.ResetAck, request.Record.Stream, 0, nil)); err != nil {
		t.Fatal(err)
	}
	if state := s.Snapshot(); state.Streams != 0 || state.ActiveDispatches != 0 || state.ReceiveReserved != 0 {
		t.Fatal("acknowledged cancellation retained logical OPEN resources", state)
	}
}

type countTargetDial struct{ calls atomic.Int32 }

func (d *countTargetDial) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	return nil, errors.New("must not dial")
}

func TestExitDenialAndPortRedirectionNeverCreateTargetSockets(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "denied", true: "wrong_port"}[redirect], func(t *testing.T) {
			a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
			ca, cb := cipherPair(t, wire.ID{94})
			dialer, err := transport.NewClientDialer(a, 256<<10)
			if err != nil {
				t.Fatal(err)
			}
			targetDial := &countTargetDial{}
			exit, err := transport.NewExitServer(b, cb, 256<<10, 2, transport.ExitPolicyFunc(func(context.Context, string, transport.Target) (netip.AddrPort, error) {
				if redirect {
					return netip.MustParseAddrPort("127.0.0.1:80"), nil
				}
				return netip.AddrPort{}, transport.OpenDenied
			}), targetDial)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(linkSessions(t, a, b, ca, cb))
			done := make(chan error, 1)
			go func() { done <- exit.Run(ctx) }()
			t.Cleanup(func() { cancel(); <-done })
			if c, err := dialer.DialContext(ctx, "tcp", "example.test:443"); c != nil || !errors.Is(err, transport.OpenDenied) {
				t.Fatal("exit policy was bypassed", err)
			}
			if targetDial.calls.Load() != 0 {
				t.Fatal("denied or redirected target dialed")
			}
		})
	}
}
