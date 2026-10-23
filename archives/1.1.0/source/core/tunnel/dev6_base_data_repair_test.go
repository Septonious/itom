package tunnel

import (
	"context"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

func TestDEV6BaseDATARepairsKnownClosedSocketBeforeAppend(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(map[bool]string{false: "bulk", true: "fast"}[fast], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cm, sm := tlsMailPair(t, ctx, []string{"up", "down"})
			m := cm.(*laneMail)
			m.baseData = true
			if err := m.Client.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := mailbox.Encode([]byte("new independently charged DATA publication"))
			if err != nil {
				t.Fatal(err)
			}
			if err = m.AppendDataPriority(ctx, "up", raw, fast); err != nil {
				t.Fatal("known dead base consumed the attempted DATA instead of repairing before APPEND", err)
			}
			rows, _, err := sm.Page(ctx, "up", mailbox.Cursor{})
			if err != nil || len(rows) != 1 {
				t.Fatal("DATA must be appended exactly once after repair", len(rows), err)
			}
		})
	}
}

func TestDEV6BaseDATALostCompletionDoesNotReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	cm, sm := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a01")
	m := cm.(*laneMail)
	m.baseData = true
	// This fixture disconnects after storing ITB1, before tagged completion.
	// The wrapper under test must preserve uncertainty and return that error.
	raw, err := mailbox.Encode([]byte("ITB1publication-with-lost-completion"))
	if err != nil {
		t.Fatal(err)
	}
	network.controlAppendDisconnect.Store(true)
	if err = m.AppendDataPriority(ctx, "up", raw, false); err == nil {
		t.Fatal("lost APPEND completion was hidden")
	}
	if network.controlAppendDisconnect.Load() {
		t.Fatal("fault did not occur")
	}
	rows, _, err := sm.Page(ctx, "up", mailbox.Cursor{})
	if err != nil || len(rows) != 1 {
		t.Fatal("uncertain DATA was replayed", len(rows), err)
	}
}
