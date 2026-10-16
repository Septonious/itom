package mailbox

import (
	"context"
	"testing"
	"time"
)

func TestPurgeFolderRemovesAllMessagesWithBoundedBatches(t *testing.T) {
	cfg, tc := testServer(t, "tls", false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-purge"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "itom-purge"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		raw, encErr := Encode([]byte{byte(i + 1)})
		if encErr != nil {
			t.Fatal(encErr)
		}
		if err = c.Append(ctx, "itom-purge", raw); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := c.PurgeFolder(ctx, "itom-purge", 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 40 {
		t.Fatalf("removed=%d", removed)
	}
	rows, _, err := c.Page(ctx, "itom-purge", Cursor{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("folder not empty rows=%d err=%v", len(rows), err)
	}
}
