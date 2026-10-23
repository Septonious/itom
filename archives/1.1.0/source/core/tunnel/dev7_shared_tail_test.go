package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

func TestDEV7CappedControlTailAvoidsRedundantSelectionAndTracksFolderSwitch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	// Reproduce the real capped role: DATA has a reader, while control receive
	// and cleanup share the base. Retire optional sockets before removing roles.
	if m.reader != nil {
		_ = m.reader.Close()
		m.reader = nil
	}
	if m.cleaner != nil {
		_ = m.cleaner.Close()
		m.cleaner = nil
	}
	_, cur, err := m.Page(ctx, "down", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		raw, _ := mailbox.Encode([]byte{byte(i + 1)})
		if err = m.controlClient().Append(ctx, "down", raw); err != nil {
			t.Fatal(err)
		}
		rows, next, e := m.Page(ctx, "down", cur)
		if e != nil || len(rows) != 1 || next.After <= cur.After {
			t.Fatal("capped tail lost row", rows, next, e)
		}
		cur = next
	}
	before, _, _ := m.controlClient().ReadCommandCounts()
	for i := 0; i < 4; i++ {
		rows, next, e := m.Page(ctx, "down", cur)
		if e != nil || len(rows) != 0 || next != cur {
			t.Fatal("quiet live tail changed", rows, next, e)
		}
	}
	after, _, _ := m.controlClient().ReadCommandCounts()
	if after != before {
		t.Fatalf("unchanged live selection renewed on every poll: %d", after-before)
	}
	// Maintenance selects the other folder. Cached validity must never permit
	// reading it as the live folder, even within the two-second audit window.
	if _, err = m.controlClient().Tail(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	raw, _ := mailbox.Encode([]byte("after maintenance"))
	if err = m.controlClient().Append(ctx, "down", raw); err != nil {
		t.Fatal(err)
	}
	rows, _, err := m.Page(ctx, "down", cur)
	if err != nil || len(rows) != 1 || rows[0].Folder != "down" || string(rows[0].MIME) != string(raw) {
		t.Fatal("maintenance selection leaked into receive", rows, err)
	}
	afterSwitch, _, _ := m.controlClient().ReadCommandCounts()
	if afterSwitch != after+2 {
		t.Fatal("missing folder-switch validity check", afterSwitch-after)
	}
}
