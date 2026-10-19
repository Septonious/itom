package tunnel

import (
	"bytes"
	"context"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

func seedPhysicalUIDHistory(t *testing.T, ctx context.Context, client *mailbox.Client, folder string, count int) {
	t.Helper()
	if err := client.Ensure(ctx, folder); err != nil {
		t.Fatal(err)
	}
	raw, _ := mailbox.Encode([]byte("previous physical session"))
	for i := 0; i < count; i++ {
		if err := client.Append(ctx, folder, raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := inventory(ctx, client, folder)
	if err != nil || len(rows) != count {
		t.Fatal("UID history inventory", len(rows), err)
	}
	for len(rows) > 0 {
		n := min(16, len(rows))
		if err = client.DeleteMany(ctx, rows[:n]); err != nil {
			t.Fatal(err)
		}
		rows = rows[n:]
	}
}

// Previous retained-state fixtures reserved quota in empty mailboxes. They did
// not retain the UID history left by real APPEND/EXPUNGE cycles.
func TestRev4FieldSparseUIDHistoryCannotStarveNewDATA(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	folder := "itom-down-data-00"
	if err := m.Client.Ensure(ctx, folder); err != nil {
		t.Fatal(err)
	}
	old, _ := mailbox.Encode([]byte("retained allocation history"))
	for i := 0; i < 64; i++ {
		if err := m.Client.Append(ctx, folder, old); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := inventory(ctx, m.Client, folder)
	if err != nil || len(rows) != 64 {
		t.Fatal("history setup", len(rows), err)
	}
	for _, row := range rows {
		if err = m.Client.Delete(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := mailbox.Encode(bytes.Repeat([]byte{0xa5}, 32<<10))
	if err = m.Client.Append(ctx, folder, want); err != nil {
		t.Fatal(err)
	}
	if _, err = m.stageTopology(ctx); err != nil {
		t.Fatal(err)
	}
	reader := m.dataReaderRef()
	if reader == nil {
		t.Fatal("split DATA reader missing")
	}
	_, before, _ := reader.ReadCommandCounts()
	readCtx, stop := context.WithTimeout(ctx, 1100*time.Millisecond)
	defer stop()
	cur := mailbox.Cursor{}
	found := 0
	for found == 0 {
		previous := cur
		cur, err = m.pageData(readCtx, folder, cur, func(row mailbox.Message) error {
			if !bytes.Equal(row.MIME, want) {
				t.Error("unexpected or changed physical BODY")
			}
			found++
			return nil
		})
		if err != nil {
			t.Fatalf("new bulk BODY stranded behind 64 expunged UIDs: cursor=%+v found=%d err=%v", cur, found, err)
		}
		if found == 0 && cur == previous {
			if _, _, err = m.waitData(readCtx, folder, 250*time.Millisecond); err != nil {
				t.Fatal("new bulk BODY stalled in historical UID holes", cur, err)
			}
		}
	}
	_, after, _ := reader.ReadCommandCounts()
	if found != 1 || after-before > 4 {
		t.Fatalf("sparse history requires a linear scan: rows=%d FETCH commands=%d", found, after-before)
	}
}
