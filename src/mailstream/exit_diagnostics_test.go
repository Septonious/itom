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
	if s != (ExitOpenSnapshot{7, 1, 1, 1, 1, 1, 1, 1}) {
		t.Fatalf("unexpected exit reason counts: %+v", s)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "secret.example") || strings.Contains(string(b), "127.0.0.1") {
		t.Fatal("destination leaked")
	}
}
