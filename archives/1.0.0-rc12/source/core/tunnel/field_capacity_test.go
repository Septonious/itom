package tunnel

import (
	"context"
	"itom.local/core/datapath"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wire"
	"strings"
	"testing"
	"time"
)

func TestFieldTCPAdmissionRefusesOneSocketWithoutEndingOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{ctx: ctx, ownerCtx: ctx, started: time.Now(), account: servercfg.Account{ID: "a01"}, cipher: cleanupFixtureEpoch(t),
		inputs: make(chan datapath.Input, 224), controlQueue: make(chan queuedControl, 32), urgentQueue: make(chan queuedControl, 32), opens: map[uint64]*opening{}}
	cfg := datapath.DefaultConfig(1, []string{"a01"})
	cfg.Limits.Streams = 1
	p, err := datapath.StartPump(ctx, datapath.PumpConfig{Engine: cfg, Account: "a01", ControlQueue: 32}, l)
	if err != nil {
		t.Fatal(err)
	}
	l.pump = p
	defer p.Close()
	app, socket := pipes()
	defer app.Close()
	if _, err = p.AddDeferredInitial(ctx, 1, socket); err != nil {
		t.Fatal(err)
	}
	target := wire.Connect{Host: "example.org", Port: 443}
	protocol, _, err := session.NewOpenRequest(1, 3, target, l.now(), transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan openResult, 1)
	l.opens[3] = &opening{request: request{ctx: ctx, target: target, id: 3, done: done}, protocol: protocol, started: time.Now()}
	reply, err := wire.EncodeControl(3, wire.ConnectResult{})
	if err != nil {
		t.Fatal(err)
	}
	if err = l.processStream(reply); err != nil {
		t.Fatal("capacity ended whole owner", err)
	}
	result := <-done
	if result.err == nil || !strings.Contains(result.err.Error(), "TCP_CAPACITY") {
		t.Fatal("new socket was not refused", result.err)
	}
	select {
	case <-p.Done():
		t.Fatal("existing socket lost owner", p.Wait())
	default:
	}
	select {
	case queued := <-l.controlQueue:
		if queued.Type != wire.Reset || queued.Stream != 3 || queued.Offset != 0 {
			t.Fatal("peer allocation was not reset", queued.Frame)
		}
	case queued := <-l.urgentQueue:
		if queued.Type != wire.Reset || queued.Stream != 3 || queued.Offset != 0 {
			t.Fatal(queued.Frame)
		}
	default:
		t.Fatal("peer allocation leaked after local capacity refusal")
	}
}
