package mailbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync/atomic"
	"testing"
)

func TestAppendHintsAreBoundedAndExactFetchStillReadsProviderBody(t *testing.T) {
	var calls atomic.Int32
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &countingFetch{s, &calls} })
	ctx := context.Background()
	c, err := dial(ctx, cfg, []string{"hinted"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "hinted"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 260; i++ {
		raw, _ := Encode([]byte{byte(i), byte(i >> 8)})
		if err = c.Append(ctx, "hinted", raw); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.appendHints) != 256 {
		t.Fatal("unbounded discovery hints", len(c.appendHints))
	}
	if hints := c.AppendHints("hinted", map[[32]byte]bool{sha256.Sum256([]byte{0, 0}): true}); len(hints) != 0 {
		t.Fatal("oldest hint not evicted")
	}
	wanted := map[[32]byte]bool{sha256.Sum256([]byte{5, 0}): true, sha256.Sum256([]byte{1, 1}): true}
	hints := c.AppendHints("hinted", wanted)
	if len(hints) != 2 || hints[0].UID == 0 || hints[0].Validity == 0 {
		t.Fatal("APPENDUID discovery missing", hints)
	}
	calls.Store(0)
	rows, err := c.FetchExact(ctx, "hinted", hints[0].Validity, []uint32{hints[0].UID, hints[1].UID})
	if err != nil || len(rows) != 2 || calls.Load() != 1 {
		t.Fatal("exact discovery did not fetch provider bodies once", len(rows), calls.Load(), err)
	}
	for _, row := range rows {
		body, err := Decode(row.MIME)
		if err != nil || !wanted[sha256.Sum256(body)] || row.Validity != hints[0].Validity {
			t.Fatal("invalid fetched body", row, err)
		}
	}
	if _, err = c.FetchExact(ctx, "hinted", hints[0].Validity+1, []uint32{hints[0].UID}); !errors.Is(err, ErrChanged) {
		t.Fatal("stale hint accepted", err)
	}
	if _, err = c.FetchExact(ctx, "hinted", hints[0].Validity, make([]uint32, 33)); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded UID batch", err)
	}
	if hints = c.AppendHints("other", wanted); len(hints) != 0 {
		t.Fatal("foreign folder hint escaped scope")
	}
}

func TestEncodedSizeMatchesMIMEAcrossLineAndBase64Boundaries(t *testing.T) {
	for _, n := range []int{1, 2, 3, 55, 56, 57, 58, 59, 76, 77, 1024, 32768, 393216, MaxBody} {
		raw, err := Encode(make([]byte, n))
		if err != nil {
			t.Fatal(err)
		}
		size, err := EncodedSize(n)
		if err != nil || size != uint64(len(raw)) {
			t.Fatal("reservation size disagrees with MIME", n, size, len(raw), err)
		}
	}
}
