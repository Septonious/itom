package session

import (
	"errors"
	"testing"
	"time"

	"itom.local/core/wire"
)

func testFrame(t *testing.T, stream uint64, m wire.Control) wire.Frame {
	t.Helper()
	f, err := wire.EncodeControl(stream, m)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func testPolicy() ControlPolicy {
	return ControlPolicy{Timeout: 10 * time.Second, RetryInterval: time.Second, MaxSends: 3}
}
func checkGateRecoversEachLostActivationMessage(t *testing.T) {
	for _, lost := range []wire.Type{wire.Confirm, wire.Ready, wire.ReadyAck} {
		checkScenarioBoundary(t, lost.String(), func(t *testing.T) {
			client, initial, err := NewGate(Client, 1, 0, testPolicy())
			if err != nil {
				t.Fatal(err)
			}
			server, _, err := NewGate(Server, 1, 0, testPolicy())
			if err != nil {
				t.Fatal(err)
			}
			dropped := false
			var deliver func(GateAction, bool, time.Duration)
			deliver = func(action GateAction, toServer bool, now time.Duration) {
				for _, f := range action.Send {
					if f.Type == lost && !dropped {
						dropped = true
						continue
					}
					target := client
					if toServer {
						target = server
					}
					reply, err := target.Receive(1, now, f)
					if err != nil {
						t.Fatal(err)
					}
					deliver(reply, !toServer, now)
				}
			}
			deliver(initial, true, 0)
			for now := time.Second; now < 5*time.Second; now += time.Second {
				a, err := client.Poll(1, now)
				if err != nil {
					t.Fatal(err)
				}
				deliver(a, true, now)
				a, err = server.Poll(1, now)
				if err != nil {
					t.Fatal(err)
				}
				deliver(a, false, now)
			}
			if !dropped || !client.Ready() || !server.Ready() {
				t.Fatal(lost, client.Phase(), server.Phase())
			}
		})
	}
}
func checkGateOpenProvesLostAckAndDataCannotBypassBarrier(t *testing.T) {
	server, _, err := NewGate(Server, 2, 0, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	open := testFrame(t, 1, wire.Connect{Host: "example.com", Port: 443})
	if _, err = server.Receive(2, 0, open); err == nil {
		t.Fatal("OPEN before CONFIRM")
	}
	if _, err = server.Receive(2, 0, testFrame(t, 0, wire.Activation{Kind: wire.Confirm})); err != nil {
		t.Fatal(err)
	}
	data := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: []byte{1}}
	if _, err = server.Receive(2, 0, data); err == nil {
		t.Fatal("unknown DATA bypassed startup")
	}
	a, err := server.Receive(2, 0, open)
	if err != nil || !a.Deliver || !a.Activated || !server.Ready() {
		t.Fatal(a, err)
	}
}
func checkGateBoundedRepliesDeadlineAndLateGeneration(t *testing.T) {
	g, _, err := NewGate(Server, 1, 0, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	confirm := testFrame(t, 0, wire.Activation{Kind: wire.Confirm})
	count := 0
	for i := 0; i < 1000; i++ {
		a, err := g.Receive(1, 0, confirm)
		if err != nil {
			t.Fatal(err)
		}
		count += len(a.Send)
	}
	for now := time.Second; now < 10*time.Second; now += time.Second {
		a, err := g.Poll(1, now)
		if err != nil {
			t.Fatal(err)
		}
		count += len(a.Send)
	}
	if count != 3 {
		t.Fatal("unbounded amplification", count)
	}
	if _, err = g.Poll(1, 10*time.Second); !errors.Is(err, ErrDeadline) {
		t.Fatal(err)
	}
	if _, err = g.Receive(1, 10*time.Second, testFrame(t, 0, wire.Activation{Kind: wire.ReadyAck})); err == nil {
		t.Fatal("resurrected")
	}
	fresh, _, err := NewGate(Client, 2, 0, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Receive(1, time.Second, testFrame(t, 0, wire.Activation{Kind: wire.Ready})); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	if fresh.Ready() {
		t.Fatal("stale activation")
	}
	if _, err = fresh.Poll(2, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Poll(2, 0); err == nil {
		t.Fatal("backward clock")
	}
}
func checkGateNoAckOfAckAndRejectsWrongRole(t *testing.T) {
	server, _, err := NewGate(Server, 1, 0, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	ack := testFrame(t, 0, wire.Activation{Kind: wire.ReadyAck})
	if _, err = server.Receive(1, 0, ack); err == nil {
		t.Fatal("unsolicited ACK")
	}
	if _, err = server.Receive(1, 0, testFrame(t, 0, wire.Activation{Kind: wire.Ready})); err == nil {
		t.Fatal("wrong role")
	}
	if _, err = server.Receive(1, 0, testFrame(t, 0, wire.Activation{Kind: wire.Confirm})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		a, err := server.Receive(1, 0, ack)
		if err != nil || len(a.Send) != 0 {
			t.Fatal(a, err)
		}
	}
}
func checkOpenBookSingleDialReplayAndTargetConflict(t *testing.T) {
	b, err := NewOpenBook(7, 4, 8, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	f := testFrame(t, 3, wire.Connect{Host: "example.com", Port: 443})
	a, err := b.Receive(7, 0, f)
	if err != nil || a.Dial == nil {
		t.Fatal(a, err)
	}
	token := a.Dial.Token
	for i := 0; i < 100; i++ {
		a, err = b.Receive(7, 0, f)
		if err != nil || a.Dial != nil || a.Reply != nil {
			t.Fatal(a, err)
		}
	}
	if b.DataAllowed(7, 3) {
		t.Fatal("data before dial completion")
	}
	changed := testFrame(t, 3, wire.Connect{Host: "other.example", Port: 443})
	if _, err = b.Receive(7, 0, changed); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	a, err = b.Complete(token, 0, wire.ReasonNone)
	if err != nil || !a.Adopt || a.Reply == nil || a.Reply.Type != wire.OpenOK {
		t.Fatal(a, err)
	}
	if !b.DataAllowed(7, 3) {
		t.Fatal("not ready")
	}
	if a, err = b.Complete(token, 0, wire.ReasonNone); err == nil || a.Adopt {
		t.Fatal("adopted twice", a, err)
	}
	a, err = b.Receive(7, time.Second, f)
	if err != nil || a.Dial != nil || a.Reply == nil || a.Reply.Type != wire.OpenOK {
		t.Fatal(a, err)
	}
	if err = b.Retire(7, 3); err != nil {
		t.Fatal(err)
	}
	a, err = b.Receive(7, 2*time.Second, f)
	if err != nil || a.Dial != nil || a.Reply == nil || a.Reply.Type != wire.OpenError || b.DataAllowed(7, 3) {
		t.Fatal(a, err)
	}
}
func checkOpenBookDeadlineCancelsAndClosesLateSuccess(t *testing.T) {
	b, err := NewOpenBook(1, 2, 4, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	f := testFrame(t, 1, wire.Connect{Host: "example.com", Port: 443})
	a, err := b.Receive(1, 0, f)
	if err != nil || a.Dial == nil {
		t.Fatal(a, err)
	}
	token := a.Dial.Token
	a, err = b.Complete(token, 10*time.Second, wire.ReasonNone)
	if err != nil || a.Adopt || a.Cancel == nil || a.Reply == nil {
		t.Fatal(a, err)
	}
	m, err := wire.DecodeControl(*a.Reply)
	if err != nil || m.(wire.ConnectResult).Code != wire.ReasonTimeout {
		t.Fatal(m, err)
	}
	if a, err = b.Complete(token, 11*time.Second, wire.ReasonNone); err == nil || a.Adopt {
		t.Fatal("late success adopted")
	}
	b.Stop()
	if a, err = b.Complete(token, 11*time.Second, wire.ReasonNone); err == nil || a.Adopt {
		t.Fatal("success after stop")
	}
	fresh, err := NewOpenBook(2, 2, 4, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if a, err = fresh.Complete(token, 11*time.Second, wire.ReasonNone); !errors.Is(err, ErrGeneration) || a.Adopt {
		t.Fatal(a, err)
	}
}
func checkOpenBookCapacityDoesNotForgetAdmittedIDs(t *testing.T) {
	// Active socket pressure must reject only the new ID and remember it even
	// after capacity frees. Metadata exhaustion below remains fail-closed.
	full, err := NewOpenBook(1, 1, 4, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	first := testFrame(t, 1, wire.Connect{Host: "example.com", Port: 443})
	if a, e := full.Receive(1, 0, first); e != nil || a.Dial == nil {
		t.Fatal(a, e)
	}
	rejected := testFrame(t, 3, wire.Connect{Host: "example.com", Port: 443})
	a, err := full.Receive(1, 0, rejected)
	if err != nil || a.Reply == nil || a.Dial != nil {
		t.Fatal("socket pressure killed session", a, err)
	}
	c, err := wire.DecodeControl(*a.Reply)
	if err != nil || c.(wire.ConnectResult).Code != wire.ReasonCapacity {
		t.Fatal(c, err)
	}
	if err := full.Retire(1, 1); err != nil {
		t.Fatal(err)
	}
	if a, err := full.Receive(1, time.Second, rejected); err != nil || a.Dial != nil || a.Reply == nil {
		t.Fatal("rejected OPEN redialled after capacity freed", a, err)
	}
	if a, err := full.Receive(1, time.Second, testFrame(t, 5, wire.Connect{Host: "example.com", Port: 443})); err != nil || a.Dial == nil {
		t.Fatal("fresh OPEN failed after capacity freed", a, err)
	}
	b, err := NewOpenBook(1, 1, 2, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 5} {
		a, err := b.Receive(1, 0, testFrame(t, id, wire.Connect{Host: "example.com", Port: 443}))
		if err != nil || a.Dial == nil {
			t.Fatal(a, err)
		}
		if err = b.Retire(1, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = b.Receive(1, 0, testFrame(t, 9, wire.Connect{Host: "example.com", Port: 443})); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	a, err = b.Receive(1, 0, testFrame(t, 1, wire.Connect{Host: "example.com", Port: 443}))
	if err != nil || a.Dial != nil || a.Reply == nil {
		t.Fatal(a, err)
	}
}
func checkOpenRequestRetryDeadlineAndNoLateResurrection(t *testing.T) {
	r, f, err := NewOpenRequest(1, 1, wire.Connect{Host: "example.com", Port: 443}, 0, testPolicy())
	if err != nil || f == nil {
		t.Fatal(err)
	}
	if r.DataAllowed() {
		t.Fatal("premature DATA")
	}
	for now := time.Second; now < 10*time.Second; now += time.Second {
		f, err = r.Poll(1, now)
		if err != nil {
			t.Fatal(err)
		}
		if (now < 3*time.Second) != (f != nil) {
			t.Fatal("retry budget", now, f)
		}
	}
	if _, err = r.Poll(1, 10*time.Second); !errors.Is(err, ErrDeadline) {
		t.Fatal(err)
	}
	if _, err = r.Receive(1, 11*time.Second, testFrame(t, 1, wire.ConnectResult{})); err == nil {
		t.Fatal("late OPEN_OK")
	}
	if r.DataAllowed() {
		t.Fatal("resurrected")
	}
}
func checkOpenRequestDuplicateResultsAndStop(t *testing.T) {
	r, _, err := NewOpenRequest(1, 1, wire.Connect{Host: "example.com", Port: 443}, 0, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	f := testFrame(t, 1, wire.ConnectResult{})
	if _, err = r.Receive(2, 0, f); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	changed, err := r.Receive(1, 0, f)
	if err != nil || !changed || !r.DataAllowed() {
		t.Fatal(changed, err)
	}
	changed, err = r.Receive(1, 0, f)
	if err != nil || changed {
		t.Fatal(changed, err)
	}
	if _, err = r.Receive(1, 0, testFrame(t, 1, wire.ConnectResult{Code: wire.ReasonRefused})); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	r.Stop()
	if r.DataAllowed() {
		t.Fatal("allowed after Stop")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioControl(t *testing.T) {
	t.Log("check=checkGateRecoversEachLostActivationMessage")
	checkGateRecoversEachLostActivationMessage(t)
	t.Log("check=checkGateOpenProvesLostAckAndDataCannotBypassBarrier")
	checkGateOpenProvesLostAckAndDataCannotBypassBarrier(t)
	t.Log("check=checkGateBoundedRepliesDeadlineAndLateGeneration")
	checkGateBoundedRepliesDeadlineAndLateGeneration(t)
	t.Log("check=checkGateNoAckOfAckAndRejectsWrongRole")
	checkGateNoAckOfAckAndRejectsWrongRole(t)
	t.Log("check=checkOpenBookSingleDialReplayAndTargetConflict")
	checkOpenBookSingleDialReplayAndTargetConflict(t)
	t.Log("check=checkOpenBookDeadlineCancelsAndClosesLateSuccess")
	checkOpenBookDeadlineCancelsAndClosesLateSuccess(t)
	t.Log("check=checkOpenBookCapacityDoesNotForgetAdmittedIDs")
	checkOpenBookCapacityDoesNotForgetAdmittedIDs(t)
	t.Log("check=checkOpenRequestRetryDeadlineAndNoLateResurrection")
	checkOpenRequestRetryDeadlineAndNoLateResurrection(t)
	t.Log("check=checkOpenRequestDuplicateResultsAndStop")
	checkOpenRequestDuplicateResultsAndStop(t)
}
