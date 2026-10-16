package tunnel

import (
	"context"
	"crypto/sha256"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

// A fully accepted but never visible first reply used to leave the client
// waiting on an obsolete handshake round while the server was already active.
// Retransmit the exact Noise hello, not a new logical session/carrier epoch.
func TestRev7LostHandshakeReplyDoesNotDelayLastAccountOrFirstOpen(t *testing.T) {
	t.Setenv("ITOM_LOAD_DROP_HELLO_REPLY", "1")
	start := time.Now()
	f := newProductionSupervisorFixtureAccounts(t, 15)
	if f.network.dropHelloReply.Load() {
		t.Fatal("missing initial-reply fault")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatal("last account waited for whole handshake timeout", elapsed)
	}
	for i := range f.dialAttempts {
		if f.dialDenied[i].Load() != 0 {
			t.Fatal("unexpected login failure")
		}
	}
	c := rev4Open(t, f)
	if err := rev4Exchange(c, 771, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("first application after lost reply", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, l := range f.serverLanes {
		total += int(l.preauthReplyRetries.Load())
	}
	if total == 0 {
		t.Fatal("cached authenticated reply was not used")
	}
	t.Logf("accounts=15 first_reply_missing=true ready_ms=%d cached_replies=%d first_application_usable=true", time.Since(start).Milliseconds(), total)
}

func TestRev7CachedHandshakeReplyCannotResetOrAmplifyCarrier(t *testing.T) {
	ctx := context.Background()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	m.Ensure(ctx, "down")
	offer := []byte(transportHelloMagic + `{"Version":10,"Reply":false}`)
	cipher := cleanupFixtureEpoch(t)
	l := &lane{ctx: ctx, side: true, mail: m, cipher: cipher, sendFolder: "down", preauthOfferDigest: sha256.Sum256(offer), preauthReply: []byte(transportHelloMagic + `{"Reply":true}`), preauthReplyUntil: time.Now().Add(time.Minute)}
	before := cipher.Context()
	deliver := func(body []byte) {
		raw, err := mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		if err = l.read([]mailbox.Message{{MIME: raw, Validity: 1, UID: 11}}); err != nil {
			t.Fatal(err)
		}
	}
	l.preauthReplyAt = time.Now().Add(-2 * time.Second)
	deliver(append(append([]byte(nil), offer...), ' '))
	if m.next["down"] != 0 {
		t.Fatal("altered offer got cached reply")
	}
	for i := 0; i < 8; i++ {
		l.preauthReplyAt = time.Now().Add(-2 * time.Second)
		deliver(offer)
	}
	if m.next["down"] != 3 || l.preauthReplyRetries.Load() != 3 {
		t.Fatal("cached reply replay amplified", m.next["down"])
	}
	if cipher.Context() != before {
		t.Fatal("duplicate offer reset authenticated cipher")
	}
	l.preauthReplyRetries.Store(0)
	l.preauthReplyUntil = time.Now().Add(-time.Second)
	deliver(offer)
	if m.next["down"] != 3 {
		t.Fatal("expired handshake cache answered replay")
	}
}
