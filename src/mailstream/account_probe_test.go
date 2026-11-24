package mailstream

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAccountProbeRequiresReturnOnOriginalMailbox(t *testing.T) {
	id := SessionID{1}
	link := &IMAPLink{receivedRoutes: map[receivedRoute]string{{id, 12}: "second", {id, 13}: "first"}}
	reply := make(chan struct{})
	s := &Session{link: link, cipher: &cipherPair{id: id}, accountPings: map[uint64]accountProbe{7: {account: "first", done: reply}}}
	frame := frame{kind: frameAccountPong, data: []byte("first"), offset: 7}
	s.handleAccountProbe(frame, 12)
	select {
	case <-reply:
		t.Fatal("accepted a reply on another mailbox")
	default:
	}
	s.handleAccountProbe(frame, 13)
	select {
	case <-reply:
	default:
		t.Fatal("correct authenticated return route was missed")
	}
	s.handleAccountProbe(frame, 13)
	if len(s.accountPings) != 0 {
		t.Fatal("finished probe still pending")
	}
}

func TestAccountProbeCancellationReleasesSlot(t *testing.T) {
	id := SessionID{3}
	secret := make([]byte, 32)
	h, hello, err := initiate(secret, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, accept, err := respond(secret, "fixture", hello)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := h.finish(accept)
	if err != nil {
		t.Fatal(err)
	}
	id = cipher.id
	link := &IMAPLink{}
	a := &mailAccountState{link: link, config: MailAccount{ID: "first"}, id: id, ready: true, fast: make(chan mailWork, 2)}
	link.accounts = []*mailAccountState{a}
	s := &Session{link: link, ctx: context.Background(), cipher: cipher}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = s.PingAccount(ctx, "first")
	if !errors.Is(err, context.DeadlineExceeded) || len(s.accountPings) != 0 {
		t.Fatal("missing reply did not expire", err)
	}
	if len(a.fast) != 1 {
		t.Fatal("probe retransmitted or did not reach its account")
	}
	if _, err = s.PingAccount(context.Background(), "missing"); !errors.Is(err, ErrProtocol) {
		t.Fatal("unknown mailbox", err)
	}
}

func TestTwoPublishersShareDurableReservation(t *testing.T) {
	store, err := openRetention(t.TempDir(), "ledger", true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	a := &mailAccountState{link: &IMAPLink{store: store}, storageKey: []byte("same-mailbox")}
	const charge = 31000
	const turns = 24
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range turns {
				a.mu.Lock()
				e := a.chargeLocked(charge)
				a.mu.Unlock()
				if e != nil {
					t.Errorf("charge: %v", e)
					return
				}
			}
		}()
	}
	wg.Wait()
	v, err := store.load(a.storageKey)
	if err != nil || v < a.spent || v != a.reserved || a.spent != 2*turns*charge || a.queuedBytes != 0 {
		t.Fatalf("ledger reserved %d persisted %d spent %d queued %d err %v", a.reserved, v, a.spent, a.queuedBytes, err)
	}
}

func TestCanonicalAndRefoldedMIMERoundTrip(t *testing.T) {
	wire := append(header(sealed, SessionID{2}, 7), make([]byte, 8193)...)
	canonical := encodeMail(wire)
	for _, body := range [][]byte{canonical, append([]byte("X-Provider: test\r\n"), canonical...)} {
		packets, err := decodeMail(body)
		if err != nil || len(packets) != 1 || !bytes.Equal(packets[0], wire) {
			t.Fatalf("MIME round trip: %v", err)
		}
	}
	if _, err := decodeMail(append(append([]byte(nil), canonical...), byte(255))); err == nil {
		t.Fatal("trailing invalid symbol accepted")
	}
}
