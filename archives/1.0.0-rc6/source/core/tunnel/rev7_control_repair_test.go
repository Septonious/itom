package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// Actual TLS/provider fault: store the full control literal, close before its
// tagged completion. The caller cannot tell whether the physical copy exists.
type rev7DisconnectControlSession struct {
	imapserver.Session
	network *providerTestNetwork
	conn    net.Conn
}

func (s *rev7DisconnectControlSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.network.suppressIdleNotifications {
		<-stop
		return nil
	}
	return s.Session.Idle(w, stop)
}

func (s *rev7DisconnectControlSession) Append(f string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	if !s.network.controlAppendDisconnect.Load() && !s.network.dropHelloReply.Load() {
		return s.Session.Append(f, r, o)
	}
	raw, err := io.ReadAll(io.LimitReader(r, mailbox.MaxMIME+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > mailbox.MaxMIME {
		return nil, mailbox.ErrLimit
	}
	body, decodeErr := mailbox.Decode(raw)
	if decodeErr == nil && bytes.HasPrefix(body, []byte(transportHelloMagic)) {
		var reply hello
		if json.Unmarshal(body[len(transportHelloMagic):], &reply) == nil && reply.Reply && s.network.dropHelloReply.CompareAndSwap(true, false) {
			return &imap.AppendData{}, nil
		}
	}
	result, err := s.Session.Append(f, bytes.NewReader(raw), o)
	if err == nil && decodeErr == nil && bytes.HasPrefix(body, []byte("ITB1")) && s.network.controlAppendDisconnect.CompareAndSwap(true, false) {
		s.conn.Close()
	}
	return result, err
}

func TestRev7AcceptedControlWithLostCompletionRetriesFreshAndKeepsDebt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	cm, sm := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a01")
	m := cm.(*laneMail)
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	store, err := ledger(t.TempDir(), "control", a, u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: ctx, account: a, user: u, control: store, mail: m, cipher: cleanupFixtureEpoch(t), sendFolder: u.Up, obsolete: map[journal.AttemptRef]bool{}, mailReceipts: map[uint64]bool{}}
	frame, err := wire.EncodeControl(0, wire.Echo{Token: 7})
	if err != nil {
		t.Fatal(err)
	}
	network.controlAppendDisconnect.Store(true)
	if err = l.writeControls([]queuedControl{{Frame: frame, added: time.Now()}}); err != nil {
		t.Fatal("lost completion destroyed control writer", err)
	}
	if network.controlAppendDisconnect.Load() {
		t.Fatal("provider fault never fired")
	}
	rows, _, err := sm.Page(ctx, u.Up, mailbox.Cursor{})
	if err != nil || len(rows) != 2 {
		t.Fatal("physical original and retry", len(rows), err)
	}
	counters := map[uint64]bool{}
	for _, row := range rows {
		body, err := mailbox.Decode(row.MIME)
		if err != nil {
			t.Fatal(err)
		}
		records, _, err := unpackControl(body)
		if err != nil || len(records) != 1 {
			t.Fatal(err)
		}
		header, err := wire.ParseHeader(records[0][:wire.HeaderSize])
		if err != nil {
			t.Fatal(err)
		}
		if counters[header.Counter] {
			t.Fatal("replayed counter prevents physical receipt for replacement")
		}
		counters[header.Counter] = true
	}
	attempts, err := allAttempts(store)
	if err != nil || len(attempts) != 2 {
		t.Fatal(attempts, err)
	}
	states := map[journal.State]int{}
	for _, a := range attempts {
		states[a.State]++
	}
	if states[journal.Unknown] != 1 || states[journal.Committed] != 1 {
		t.Fatal("original not uncertain, replacement not independent", states)
	}
	held, err := store.Held(a.ID, u.ID)
	if err != nil || held.Messages != 2 {
		t.Fatal("uncertain physical debt forgiven", held, err)
	}
	if len(l.out) != 2 || l.out[0].ref == l.out[1].ref {
		t.Fatal("original no longer discoverable for receipt/cleanup", l.out)
	}
	if err = store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRev7CloseDuringControlRepairCannotPublishOrLeakSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	dial := m.dial
	entered, release := make(chan struct{}), make(chan struct{})
	var fresh atomic.Pointer[mailbox.Client]
	m.dial = func(call context.Context) (*mailbox.Client, error) {
		close(entered)
		<-release
		c, e := dial(call)
		if c != nil {
			fresh.Store(c)
		}
		return c, e
	}
	m.Client.Close()
	done := make(chan error, 1)
	go func() { done <- m.repairControl(ctx) }()
	<-entered
	m.Close()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("closed mail published repair", err)
	}
	if c := fresh.Load(); c == nil || c.Ready() {
		t.Fatal("late socket leaked")
	}
}

func TestRev7ControlRepairDoesNotRetryScopeOrParentCancellation(t *testing.T) {
	for _, err := range []error{context.Canceled, mailbox.ErrChanged, mailbox.ErrScope, mailbox.ErrLimit, errors.New("IMAP_AUTH_OR_GREETING_FAILED")} {
		if repairableControlFailure(err) {
			t.Fatal("non-repairable failure hidden", err)
		}
	}
}
