package mailcontrol

import (
	"bytes"
	"context"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func epochs(t *testing.T, sid wire.ID) (*secure.Epoch, *secure.Epoch) {
	t.Helper()
	b := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: sid, Handshake: wire.ID{3}, Epoch: 1, ConfigHash: [32]byte{4}}
	left, err := secure.NewHandshake(b, bytes.Repeat([]byte{9}, 32), true)
	if err != nil {
		t.Fatal(err)
	}
	right, err := secure.NewHandshake(b, bytes.Repeat([]byte{9}, 32), false)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := left.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = right.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, err = right.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = left.Read(msg); err != nil {
		t.Fatal(err)
	}
	l, err := left.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	r, err := right.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	return l, r
}

type fakeMail struct {
	mailbox.Transport
	sent                   [][]byte
	appendErr, errorDelete error
	absent                 bool
}

func (m *fakeMail) Append(_ context.Context, _ string, b []byte) error {
	m.sent = append(m.sent, append([]byte(nil), b...))
	return m.appendErr
}
func (m *fakeMail) Delete(_ context.Context, _ mailbox.Message) error {
	m.absent = true
	return m.errorDelete
}
func (m *fakeMail) Absent(context.Context, string, uint32, uint32) (bool, error) {
	return m.absent, nil
}
func fixture(t *testing.T) (*Sender, *secure.Epoch, *journal.Store, *fakeMail, string, journal.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := journal.Config{MaxGrants: 4, MaxAttempts: 4, Accounts: []journal.Account{{ID: "a01", Provider: "test", Limit: pool.Amount{Bytes: 65536, Messages: 2}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 65536, Messages: 2}, Folders: []string{"up", "down"}}}}}}
	j, e := journal.Open(path, cfg, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { j.Close() })
	if e = j.EnableAfterInventory("a01", "12345678", j.Run()); e != nil {
		t.Fatal(e)
	}
	tx, rx := epochs(t, wire.ID{5})
	mail := &fakeMail{}
	s, e := NewSender(j, journal.Scope{Account: "a01", User: "12345678", Session: wire.ID{5}}, "up", tx, mail)
	if e != nil {
		t.Fatal(e)
	}
	return s, rx, j, mail, path, cfg
}
func ping(t *testing.T) wire.Frame {
	t.Helper()
	f, e := wire.EncodeControl(0, wire.Echo{Token: 42})
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func message(m *fakeMail, n int) mailbox.Message {
	return mailbox.Message{Folder: "up", Validity: 1, UID: uint32(n + 1), MIME: m.sent[n]}
}
func TestControlAuthenticationQuotaAndReclamation(t *testing.T) {
	s, rx, j, m, _, _ := fixture(t)
	ctx := context.Background()
	first := s.Send(ctx, ping(t))
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	changed := append([]byte(nil), m.sent[0]...)
	packet, e := mailbox.Decode(changed)
	if e != nil {
		t.Fatal(e)
	}
	packet[len(packet)-1] ^= 1
	changed, _ = mailbox.Encode(packet)
	if _, e = Open(changed, rx); e == nil {
		t.Fatal("tamper accepted")
	}
	frame, e := Open(m.sent[0], rx)
	if e != nil || frame.Type != wire.Ping {
		t.Fatal(e)
	}
	if _, e = Open(m.sent[0], rx); e == nil {
		t.Fatal("replay accepted")
	}
	second := s.Send(ctx, ping(t))
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if r := s.Send(ctx, ping(t)); r.Err == nil || r.Attempt != nil || len(m.sent) != 2 {
		t.Fatal("quota bypass", r)
	}
	foreign := message(m, 1)
	if e = s.Reclaim(ctx, *first.Attempt, foreign); e == nil {
		t.Fatal("wrong ciphertext accepted")
	}
	s.Stop() // session fenced; physical records are now obsolete.
	if e = s.Reclaim(ctx, *first.Attempt, message(m, 0)); e != nil {
		t.Fatal(e)
	}
	if e = s.Reclaim(ctx, *second.Attempt, message(m, 1)); e != nil {
		t.Fatal(e)
	}
	rows, e := j.ScanAttempts(0, 16)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if r := s.Send(ctx, ping(t)); r.Err != ErrStopped {
		t.Fatal(r.Err)
	}
}
func TestControlUnknownAppendSurvivesRestartWithoutRetryOrRefund(t *testing.T) {
	s, _, j, m, path, cfg := fixture(t)
	m.appendErr = mailbox.ErrUnknown
	r := s.Send(context.Background(), ping(t))
	if !errors.Is(r.Err, mailbox.ErrUnknown) || r.Attempt == nil || len(m.sent) != 1 {
		t.Fatal(r)
	}
	s.Stop()
	if e := j.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := journal.Open(path, cfg, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	row, e := reopened.GetAttempt(*r.Attempt)
	if e != nil || row.State != journal.Unknown || row.Location.UID != 0 {
		t.Fatal(row, e)
	}
	recovery, e := NewSender(reopened, s.scope, s.folder, s.cipher, m)
	if e != nil {
		t.Fatal(e)
	}
	m.absent = true
	if e = recovery.RecoverDeletion(context.Background(), *r.Attempt); e != ErrScope {
		t.Fatal("unknown locator refunded", e)
	}
	g, e := reopened.GetGrant(r.Attempt.Grant)
	if e != nil || g.Released.Messages != 0 || g.Used.Messages != 1 || !g.Closed || len(m.sent) != 1 {
		t.Fatal(g, e)
	}
}
func TestControlCrashAfterDeleteRecoversWithObservedLocator(t *testing.T) {
	s, _, j, m, path, cfg := fixture(t)
	ctx := context.Background()
	r := s.Send(ctx, ping(t))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	s.Stop()
	m.errorDelete = errors.New("lost completion after EXPUNGE")
	if e := s.Reclaim(ctx, *r.Attempt, message(m, 0)); e == nil {
		t.Fatal("expected injected failure")
	}
	if e := j.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := journal.Open(path, cfg, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	recovery, e := NewSender(reopened, s.scope, s.folder, s.cipher, m)
	if e != nil {
		t.Fatal(e)
	}
	if e = recovery.RecoverDeletion(ctx, *r.Attempt); e != nil {
		t.Fatal(e)
	}
	rows, e := reopened.ScanAttempts(0, 16)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
}
func TestControlRejectsDataWrongScopeAndCanceledSend(t *testing.T) {
	s, _, j, m, _, _ := fixture(t)
	f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: []byte("data")}
	if r := s.Send(context.Background(), f); r.Err == nil {
		t.Fatal("DATA spent control reserve")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := s.Send(ctx, ping(t)); !errors.Is(r.Err, context.Canceled) {
		t.Fatal(r.Err)
	}
	bad := s.scope
	bad.User = "87654321"
	if _, e := NewSender(j, bad, "up", s.cipher, m); e != ErrScope {
		t.Fatal(e)
	}
	rows, e := j.ScanAttempts(0, 16)
	if e != nil || len(rows) != 0 || len(m.sent) != 0 {
		t.Fatal(rows, e)
	}
}

func TestForgedControlRoutingMarkerDoesNotConsumeDataReplayCounter(t *testing.T) {
	s, rx, _, _, _, _ := fixture(t)
	f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: []byte("protected data")}
	packet, e := s.cipher.Seal(f)
	if e != nil {
		t.Fatal(e)
	}
	mime, e := mailbox.Encode(append([]byte("ITC1"), packet...))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Open(mime, rx); e == nil {
		t.Fatal("DATA accepted as control")
	}
	got, e := rx.Open(packet)
	if e != nil || !bytes.Equal(got.Payload, f.Payload) {
		t.Fatal("routing hint consumed DATA", e)
	}
}
