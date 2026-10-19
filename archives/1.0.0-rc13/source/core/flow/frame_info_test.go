package flow

import (
	"errors"
	"testing"
)

func TestFrameInfoDoesNotCopyOrExposePayload(t *testing.T) {
	tx := transmitter(t)
	id, a := begin(t, tx)
	f, n, err := tx.FrameInfo(id)
	if err != nil || n != 4 || f.Payload != nil || f.Logical != id || f.Stream != 1 {
		t.Fatal(f, n, err)
	}
	if allocs := testing.AllocsPerRun(100, func() { _, _, _ = tx.FrameInfo(id) }); allocs != 0 {
		t.Fatal("metadata allocates", allocs)
	}
	full, _ := tx.Frame(id)
	full.Payload[0] = 'X'
	again, _ := tx.Frame(id)
	if string(again.Payload) != "abcd" {
		t.Fatal("payload exposed")
	}
	if err = tx.Complete(a.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Receipt(1, 4); err != nil {
		t.Fatal(err)
	}
	if _, _, err = tx.FrameInfo(id); !errors.Is(err, ErrUnknown) {
		t.Fatal("retired logical metadata", err)
	}
	if err = tx.Check(); err != nil {
		t.Fatal(err)
	}
}
