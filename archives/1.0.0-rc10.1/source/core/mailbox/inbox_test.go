package mailbox

import (
	"context"
	"errors"
	"testing"
)

type inboxTransport struct {
	Transport
	calls int
	rows  []Message
	err   error
}

func (m *inboxTransport) Page(context.Context, string, Cursor) ([]Message, Cursor, error) {
	m.calls++
	return append([]Message(nil), m.rows...), Cursor{Validity: 1, After: 2}, m.err
}
func checkInboxBackpressureAndOwnershipTokens(t *testing.T) {
	mail := &inboxTransport{rows: []Message{{Folder: "up", Validity: 1, UID: 1, MIME: []byte("a")}, {Folder: "up", Validity: 1, UID: 2, MIME: []byte("b")}}}
	i, _ := NewInbox(mail, "up", Cursor{})
	other, _ := NewInbox(mail, "up", Cursor{})
	first, e := i.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = i.Next(context.Background()); e != ErrPending {
		t.Fatal(e)
	}
	foreign, e := other.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = i.Accept(foreign.Token); e != ErrScope {
		t.Fatal("cross-inbox token accepted")
	}
	if mail.calls != 2 || i.Cursor() != (Cursor{}) {
		t.Fatal("cursor advanced before owner acceptance")
	}
	if e = i.Accept(first.Token); e != nil {
		t.Fatal(e)
	}
	if e = i.Accept(first.Token); e != ErrScope {
		t.Fatal("token accepted twice")
	}
	second, e := i.Next(context.Background())
	if e != nil || string(second.Message.MIME) != "b" || mail.calls != 2 {
		t.Fatal(second, e)
	}
	if i.Cursor() != (Cursor{}) {
		t.Fatal("page advanced early")
	}
	if e = i.Accept(second.Token); e != nil || i.Cursor() != (Cursor{Validity: 1, After: 2}) {
		t.Fatal(e)
	}
	// Accept drops only inbox references; caller's ownership is unaffected.
	if string(first.Message.MIME) != "a" {
		t.Fatal("accepted buffer corrupted")
	}
}
func checkInboxIOFailureAndCancellationPreserveCursor(t *testing.T) {
	mail := &inboxTransport{err: errors.New("fetch failed")}
	cur := Cursor{Validity: 1, After: 1}
	i, _ := NewInbox(mail, "up", cur)
	if _, e := i.Next(context.Background()); e == nil || i.Cursor() != cur {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := i.Next(ctx); !errors.Is(e, context.Canceled) || mail.calls != 1 {
		t.Fatal(e)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioInbox(t *testing.T) {
	t.Log("check=checkInboxBackpressureAndOwnershipTokens")
	checkInboxBackpressureAndOwnershipTokens(t)
	t.Log("check=checkInboxIOFailureAndCancellationPreserveCursor")
	checkInboxIOFailureAndCancellationPreserveCursor(t)
}
