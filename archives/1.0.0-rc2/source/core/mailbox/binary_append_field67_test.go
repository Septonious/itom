package mailbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestField67BinaryAppendRoundTripAndCanonicalCharge(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapBinary: {}}
	cfg, tlsCfg := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sender, err := dial(ctx, cfg, []string{"itom-binary"}, tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	reader, err := dial(ctx, cfg, []string{"itom-binary"}, tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if !sender.BinaryAppendEnabled() {
		t.Fatal("advertised BINARY not enabled automatically")
	}
	if err = sender.Ensure(ctx, "itom-binary"); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 32768)
	if _, err = rand.Read(body); err != nil {
		t.Fatal(err)
	}
	body[0], body[1], body[2], body[3] = 0, 255, 13, 10
	canonical, err := Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := binaryPhysical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(physical) >= len(canonical)*3/4+128 {
		t.Fatalf("no physical saving: %d >= %d", len(physical), len(canonical))
	}
	if n, err := CanonicalEncodedBytes(physical); err != nil || n != uint64(len(canonical)) {
		t.Fatalf("canonical charge: %d %v want %d", n, err, len(canonical))
	}
	if err = sender.Append(ctx, "itom-binary", canonical); err != nil {
		t.Fatal(err)
	}
	rows, _, err := reader.Page(ctx, "itom-binary", Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	if !bytes.Equal(rows[0].MIME, canonical) {
		t.Fatal("BINARY read not normalized to canonical journal evidence")
	}
	got, err := Decode(rows[0].MIME)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("decoded binary mismatch: %v", err)
	}
	// Fast BINARY[1] streaming must produce identical canonical evidence.
	streamed, _, err := reader.PageTailStream(ctx, "itom-binary", Cursor{}, true, 4, func(Message) error { return nil })
	if err != nil || len(streamed) != 1 || !bytes.Equal(streamed[0].MIME, canonical) {
		t.Fatalf("binary stream canonical mismatch: %v rows=%d", err, len(streamed))
	}
	// Exact UID proof and eventual cleanup must work on mixed physical formats.
	exact, err := reader.FetchExact(ctx, "itom-binary", rows[0].Validity, []uint32{rows[0].UID})
	if err != nil || len(exact) != 1 || !bytes.Equal(exact[0].MIME, canonical) {
		t.Fatalf("exact proof mismatch: %v", err)
	}
	if err := reader.Delete(ctx, rows[0]); err != nil {
		t.Fatal(err)
	}
	absent, err := reader.Absent(ctx, "itom-binary", rows[0].Validity, rows[0].UID)
	if err != nil || !absent {
		t.Fatalf("delete/absence: %v %v", err, absent)
	}
}

func TestField67BinaryAppendUnadvertisedFallsBack(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}
	cfg, tlsCfg := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sender, err := dial(ctx, cfg, []string{"itom-mime"}, tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if sender.BinaryAppendEnabled() {
		t.Fatal("BINARY enabled without capability")
	}
	if err = sender.Ensure(ctx, "itom-mime"); err != nil {
		t.Fatal(err)
	}
	raw, err := Encode([]byte{0, 1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.Append(ctx, "itom-mime", raw); err != nil {
		t.Fatal(err)
	}
	rows, _, err := sender.Page(ctx, "itom-mime", Cursor{})
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].MIME, raw) {
		t.Fatalf("MIME fallback failed: %v rows=%d", err, len(rows))
	}
}

func TestField67BinaryAppendAccountOptOut(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapBinary: {}}
	cfg, tlsCfg := testServerWithCaps(t, "tls", caps, nil)
	cfg.DisableBinaryAppend = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sender, err := dial(ctx, cfg, []string{"itom-default"}, tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if sender.BinaryAppendEnabled() {
		t.Fatal("BINARY enabled despite account opt-out")
	}
}

// A tagged NO for a literal8 APPEND changes policy for subsequent sends, but
// the first uncertain attempt must NEVER be transparently replayed.
type rejectFirstBinaryAppend struct {
	imapserver.Session
	rejected *atomic.Bool
}

func (s rejectFirstBinaryAppend) Append(folder string, r imap.LiteralReader, opts *imap.AppendOptions) (*imap.AppendData, error) {
	if !s.rejected.Swap(true) {
		_, _ = io.Copy(io.Discard, r)
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnknownCTE}
	}
	return s.Session.Append(folder, r, opts)
}
func TestField67BinaryRejectDisablesFutureBinaryWithoutReplay(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapBinary: {}}
	var rejected atomic.Bool
	cfg, tlsCfg := testServerWithCaps(t, "tls", caps, func(s imapserver.Session) imapserver.Session { return rejectFirstBinaryAppend{s, &rejected} })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-downgrade"}, tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "itom-downgrade"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte{0, 255, 1, 17, 19, 0})
	err = c.Append(ctx, "itom-downgrade", raw)
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("refused APPEND must be unknown, got %v", err)
	}
	if c.BinaryAppendEnabled() {
		t.Fatal("tagged NO did not disable binary for future APPEND")
	}
	// An uncertain APPEND fences this socket; a newly authenticated socket
	// retains the account-specific BINARY downgrade, never resending the old
	// message on its own.
	fresh, dialErr := dial(ctx, cfg, []string{"itom-downgrade"}, tlsCfg)
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	defer fresh.Close()
	if fresh.BinaryAppendEnabled() {
		t.Fatal("reconnect forgot account-scoped BINARY downgrade")
	}
	if err = fresh.Append(ctx, "itom-downgrade", raw); err != nil {
		t.Fatal(err)
	}
	rows, _, err := fresh.Page(ctx, "itom-downgrade", Cursor{})
	if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].MIME, raw) {
		t.Fatalf("unexpected replay or MIME fallback failure: %v rows %d", err, len(rows))
	}
}
