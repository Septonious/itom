package mailstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRC16ExitOpenDiagnostics(t *testing.T) {
	_, err := PublicDial(context.Background(), "tcp", "127.0.0.1:443")
	if !errors.Is(err, ErrExitPolicy) || !errors.Is(err, ErrAuth) {
		t.Fatalf("private-target rejection changed: %v", err)
	}
	var c exitOpenCounters
	for _, item := range []struct {
		err     error
		expired bool
		ctx     error
	}{
		{nil, false, nil}, {err, false, nil}, {fmt.Errorf("wrapped: %w", ErrExitDNS), false, nil}, {ErrExitConnect, false, nil}, {nil, true, context.Canceled}, {context.Canceled, false, context.Canceled}, {errors.New("secret.example"), false, nil},
	} {
		c.attempts.Add(1)
		c.record(item.err, item.expired, item.ctx)
	}
	s := c.snapshot()
	if s != (ExitOpenSnapshot{Attempts: 7, Established: 1, Policy: 1, DNS: 1, Connect: 1, Timeout: 1, Canceled: 1, Other: 1}) {
		t.Fatalf("unexpected exit reason counts: %+v", s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "secret.example") || strings.Contains(string(b), "127.0.0.1") {
		t.Fatal("destination leaked")
	}
}

func TestRC2RelayStageServerCounters(t *testing.T) {
	var c exitOpenCounters
	for _, err := range []error{ErrExitRelayDial, ErrExitRelayTLS, ErrExitRelaySetup, ErrExitRelayCooldown} {
		c.attempts.Add(1)
		c.record(err, false, nil)
	}
	if got := c.snapshot(); got != (ExitOpenSnapshot{Attempts: 4, RelayDial: 1, RelayTLS: 1, RelaySetup: 1, RelayCooldown: 1}) {
		t.Fatalf("incorrect relay counters: %+v", got)
	}
}
