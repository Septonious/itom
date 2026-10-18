package session

import (
	"itom.local/core/wire"
	"testing"
	"time"
)

func checkBootstrapWaitsForDurableWalletAndRecoversLostAck(t *testing.T) {
	store := wire.ID{9}
	server, e := NewBootstrap(Server, 1, 0, store, 7)
	if e != nil {
		t.Fatal(e)
	}
	client, e := NewBootstrap(Client, 1, 0, store, 0)
	if e != nil {
		t.Fatal(e)
	}
	offer, e := server.Poll(1, 0)
	if e != nil || offer.Send == nil {
		t.Fatal(e)
	}
	propose, e := client.Receive(1, 0, *offer.Send)
	if e != nil || propose.Proposed == nil || propose.Send != nil || client.Ready() {
		t.Fatal("accepted before durable bind", propose, e)
	}
	if _, e = client.CommitStore(1, 0, wire.ID{8}); e == nil {
		t.Fatal("wrong store")
	}
	first, e := client.CommitStore(1, 0, store)
	if e != nil || first.Send == nil || !client.Ready() {
		t.Fatal(e)
	}
	// Lose first ack. Server keeps re-sending its original offer.
	if server.Ready() {
		t.Fatal("server ready before ack")
	}
	offer, e = server.Poll(1, 2*time.Second)
	if e != nil || offer.Send == nil {
		t.Fatal(e)
	}
	second, e := client.Receive(1, 2*time.Second, *offer.Send)
	if e != nil || second.Send == nil {
		t.Fatal(e)
	}
	ack, e := server.Receive(1, 2*time.Second, *second.Send)
	if e != nil || !ack.Ready || !server.Ready() {
		t.Fatal(e)
	}
	if _, e = client.Receive(2, 2*time.Second, *offer.Send); e != ErrGeneration {
		t.Fatal(e)
	}
	bad, _ := wire.EncodeControl(0, wire.Coordinator{Store: wire.ID{8}, Run: 7, Features: 1})
	if _, e = client.Receive(1, 3*time.Second, bad); e == nil {
		t.Fatal("store replacement")
	}
	server.Stop()
	if server.Ready() {
		t.Fatal("ready after stop")
	}
}
func checkBootstrapDeadlineAndAckForgery(t *testing.T) {
	s, _ := NewBootstrap(Server, 1, 0, wire.ID{9}, 7)
	bad, _ := wire.EncodeControl(0, wire.Coordinator{Store: wire.ID{9}, Run: 8, Features: 1, Ack: true})
	if _, e := s.Receive(1, 0, bad); e == nil {
		t.Fatal("wrong run ack")
	}
	if _, e := s.Poll(1, 45*time.Second); e != ErrDeadline {
		t.Fatal(e)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioBootstrap(t *testing.T) {
	t.Log("check=checkBootstrapWaitsForDurableWalletAndRecoversLostAck")
	checkBootstrapWaitsForDurableWalletAndRecoversLostAck(t)
	t.Log("check=checkBootstrapDeadlineAndAckForgery")
	checkBootstrapDeadlineAndAckForgery(t)
}
