package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"itom.local/core/transport"
	"itom.local/core/wallet"
)

func exchangeDrivenBootstrap(t *testing.T, f *bootstrapFixture) (*transport.BootstrapHandoff, *transport.BootstrapHandoff) {
	t.Helper()
	client, err := transport.NewBootstrapDriver(f.client, f.cx, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := transport.NewBootstrapDriver(f.server, f.sx, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		client bool
		h      *transport.BootstrapHandoff
		err    error
	}
	done := make(chan result, 2)
	go func() { h, err := server.Run(ctx); done <- result{false, h, err} }()
	go func() { h, err := client.Run(ctx); done <- result{true, h, err} }()
	var ch, sh *transport.BootstrapHandoff
	for range 2 {
		r := <-done
		if r.err != nil {
			c, _ := json.Marshal(client.Snapshot())
			s, _ := json.Marshal(server.Snapshot())
			t.Logf("bootstrap client=%s server=%s", c, s)
			t.Fatal("independent bootstrap", r.client, r.err)
		}
		if r.client {
			ch = r.h
		} else {
			sh = r.h
		}
	}
	for _, d := range []*transport.BootstrapDriver{client, server} {
		s := d.Snapshot()
		if !s.HandoffComplete || s.Prepared != 3 || s.Accepted != 3 || s.Uncertain != 0 || s.OwnObserved != 3 || s.PeerObserved != 3 || s.ElapsedNS <= 0 || len(s.Events) > 64 {
			t.Fatal("startup diagnostics contradict actual six-message exchange", s)
		}
		// Diagnostic observers receive a detached bounded event list.
		if len(s.Events) > 0 {
			s.Events[0].Stage = "changed observer copy"
			if d.Snapshot().Events[0].Stage == s.Events[0].Stage {
				t.Fatal("diagnostic snapshot exposes mutable startup state")
			}
		}
	}
	// Completed startup leaves the original selected carriers healthy.
	for _, x := range []*transport.Executor{f.cx, f.sx} {
		command, err := x.Noop(ctx)
		if err != nil {
			t.Fatal("startup shutdown closed transferred carrier", err)
		}
		if _, err = command.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ch, sh
}

func TestCanceledBootstrapDriverKeepsCarrierAndResumesOnlyAfterARealGrant(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, false)
	c, err := transport.NewBootstrapDriver(f.client, f.cx, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := transport.NewBootstrapDriver(f.server, f.sx, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Run(ctx); done <- err }()
	deadline := time.After(3 * time.Second)
	for s.Snapshot().DiscoveryTurns == 0 {
		select {
		case <-deadline:
			t.Fatal("server never began actual inventory")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err = s.Run(context.Background()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("concurrent startup Run accepted", err)
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if state := s.Snapshot(); state.Phase != transport.ServiceStopped || state.Failure != "canceled" || state.HandoffComplete || state.Prepared != 0 {
		t.Fatal("canceled discovery invented connected state", state)
	}
	if _, err = c.Run(context.Background()); !errors.Is(err, wallet.ErrExhausted) {
		t.Fatal(err)
	}
	acceptBootstrapGrant(t, f)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _, err := s.Run(ctx); done <- err }()
	if _, err = c.Run(ctx); err != nil {
		t.Fatal("initial permission failed to resume original client startup", err, c.Snapshot(), s.Snapshot())
	}
	if err = <-done; err != nil {
		t.Fatal("canceled server startup lost its actual carrier/state", err, s.Snapshot())
	}
	c.Close()
	s.Close()
	for _, x := range []*transport.Executor{f.cx, f.sx} {
		cmd, err := x.Noop(ctx)
		if err != nil {
			t.Fatal("completed startup owner closed runtime carrier", err)
		}
		if _, err = cmd.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentBootstrapDriversEstablishKeysWithoutFixturePhaseScheduling(t *testing.T) {
	for _, fluctuate := range []bool{false, true} {
		t.Run(map[bool]string{false: "immediate", true: "variable_command_service"}[fluctuate], func(t *testing.T) {
			var clientService, serverService []time.Duration
			if fluctuate {
				clientService = []time.Duration{10 * time.Millisecond, 50 * time.Millisecond}
				serverService = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}
			}
			f := bootstrapFixtureWithService(t, true, clientService, serverService, false)
			exchangeDrivenBootstrap(t, f)
			if err := f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBootstrapDriverCannotCompeteWithManualAdmissionAndMissingPermissionDoesNotWrite(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, false)
	d, err := transport.NewBootstrapDriver(f.client, f.cx, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := transport.NewBootstrapDriver(f.client, f.cx, transport.DefaultInboxServiceLimits()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("two startup owners claimed one bootstrap/socket", err)
	}
	if _, err := f.client.Prepare(context.Background()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("manual publisher competed with startup driver", err)
	}
	if _, err := f.client.Inbox(f.cx, transport.DefaultInboxLimits()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("manual inbox competed with startup driver", err)
	}
	if _, err := f.client.Handoff(); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("manual handover competed with startup driver", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := d.Run(ctx); !errors.Is(err, wallet.ErrExhausted) {
		t.Fatal("missing permission did not return to the real authority", err)
	}
	s := d.Snapshot()
	if s.Failure != "permission" || s.Prepared != 0 || s.Accepted != 0 || s.Uncertain != 0 || s.HandoffComplete {
		t.Fatal("startup invented permission or connected state", s)
	}
	f.box.mu.Lock()
	count := len(f.box.bodies)
	f.box.mu.Unlock()
	if count != 0 || f.client.Pending() != nil {
		t.Fatal("unaccounted initial mail reached provider", count)
	}
}
