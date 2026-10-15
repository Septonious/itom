package integration

import (
	"testing"
	"time"

	"itom.local/core/secure"
	"itom.local/core/session"
	"itom.local/core/wire"
)

func throughNoise(t *testing.T, tx, rx *secure.Epoch, f wire.Frame) wire.Frame {
	t.Helper()
	packet, err := tx.Seal(f)
	if err != nil {
		t.Fatal(err)
	}
	result, err := rx.Open(packet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rx.Open(packet); err != secure.ErrReplay {
		t.Fatal("physical replay accepted", err)
	}
	return result
}
func checkNoiseActivationAndOpenWithLostAckAndLostOpenOK(t *testing.T) {
	left, right := epochs(t)
	policy := session.DefaultControlPolicy()
	client, initial, err := session.NewGate(session.Client, 1, 0, policy)
	if err != nil {
		t.Fatal(err)
	}
	server, _, err := session.NewGate(session.Server, 1, 0, policy)
	if err != nil {
		t.Fatal(err)
	}
	a, err := server.Receive(1, 0, throughNoise(t, left, right, initial.Send[0]))
	if err != nil || len(a.Send) != 1 {
		t.Fatal(a, err)
	}
	a, err = client.Receive(1, 0, throughNoise(t, right, left, a.Send[0]))
	if err != nil || !client.Ready() || len(a.Send) != 1 {
		t.Fatal(a, err)
	}
	// READY_ACK is lost. Client may issue OPEN after READY.
	request, open, err := session.NewOpenRequest(1, 1, wire.Connect{Host: "example.com", Port: 443}, 0, policy)
	if err != nil {
		t.Fatal(err)
	}
	book, err := session.NewOpenBook(1, 128, 4096, policy)
	if err != nil {
		t.Fatal(err)
	}
	f := throughNoise(t, left, right, *open)
	a, err = server.Receive(1, 0, f)
	if err != nil || !a.Activated || !a.Deliver || !server.Ready() {
		t.Fatal(a, err)
	}
	action, err := book.Receive(1, 0, f)
	if err != nil || action.Dial == nil {
		t.Fatal(action, err)
	}
	token := action.Dial.Token
	action, err = book.Complete(token, time.Second, wire.ReasonNone)
	if err != nil || !action.Adopt || action.Reply == nil {
		t.Fatal(action, err)
	}
	// The first OPEN_OK is lost. Retry OPEN, never the underlying dial.
	if request.DataAllowed() {
		t.Fatal("client sent data before OPEN_OK")
	}
	open, err = request.Poll(1, 3*time.Second)
	if err != nil || open == nil {
		t.Fatal(err)
	}
	f = throughNoise(t, left, right, *open)
	action, err = book.Receive(1, 3*time.Second, f)
	if err != nil || action.Dial != nil || action.Reply == nil || action.Adopt {
		t.Fatal(action, err)
	}
	f = throughNoise(t, right, left, *action.Reply)
	changed, err := request.Receive(1, 3*time.Second, f)
	if err != nil || !changed || !request.DataAllowed() {
		t.Fatal(changed, err)
	}
	// Logical duplicate uses fresh ciphertext and returns the cached outcome.
	openFrame, err := wire.EncodeControl(1, wire.Connect{Host: "example.com", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	action, err = book.Receive(1, 5*time.Second, throughNoise(t, left, right, openFrame))
	if err != nil || action.Dial != nil || action.Reply == nil {
		t.Fatal(action, err)
	}
	changed, err = request.Receive(1, 5*time.Second, throughNoise(t, right, left, *action.Reply))
	if err != nil || changed {
		t.Fatal(changed, err)
	}
	book.Stop()
	request.Stop()
	client.Stop()
	server.Stop()
	action, err = book.Complete(token, 6*time.Second, wire.ReasonNone)
	if err == nil || action.Adopt || request.DataAllowed() {
		t.Fatal("resurrection after Stop")
	}
}

func checkControlValidationAfterAuthentication(t *testing.T) {
	left, right := epochs(t)
	server, _, err := session.NewGate(session.Server, 1, 0, session.DefaultControlPolicy())
	if err != nil {
		t.Fatal(err)
	}
	// AEAD validity does not make a malformed control payload admissible.
	bad := wire.Frame{Type: wire.Confirm, Payload: []byte{1}}
	decoded := throughNoise(t, left, right, bad)
	if _, err = server.Receive(1, 0, decoded); err == nil || server.Phase() != session.AwaitConfirm {
		t.Fatal("malformed CONFIRM changed phase", err)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioControl(t *testing.T) {
	t.Log("check=checkNoiseActivationAndOpenWithLostAckAndLostOpenOK")
	checkNoiseActivationAndOpenWithLostAckAndLostOpenOK(t)
	t.Log("check=checkControlValidationAfterAuthentication")
	checkControlValidationAfterAuthentication(t)
}
