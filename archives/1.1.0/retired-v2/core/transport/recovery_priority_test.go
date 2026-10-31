package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/pool"
)

// Scheduler/authority component check. Public endpoint tests separately exercise
// original physical tickets, authenticated OPEN, DATA echo, HTTPS and recovery.
func TestHistoricalRecoveryYieldsToLiveRecordsAndUnknownWorkCannotFloodControl(t *testing.T) {
	endpoint, s, current := endpointPolicyFixture(t)
	owner := endpoint.Service.Authorities["account1"]
	old := policyOldGrant(t, owner.routes[ControlLedger].store, pool.Amount{Bytes: 4096, Messages: 1})
	for range maxHistoricalAuthorityQueued {
		if e := owner.queue(ControlLedger, old); e != nil {
			t.Fatal(e)
		}
	}
	if e := owner.queue(ControlLedger, old); !errors.Is(e, ErrCapacity) {
		t.Fatal("history bypassed bounded queued/in-flight admission", e)
	}
	if e := s.Add(1, 262144, 262144); e != nil {
		t.Fatal(e)
	}
	if e := s.Queue(1, []byte("live request"), time.Now()); e != nil {
		t.Fatal(e)
	}
	live, ok := s.TakeFor(Fast, MaxPayload, AnyLedger)
	if !ok || live.Record.Kind != Data {
		t.Fatal("historical metadata delayed live record", ok, live)
	}
	if e := live.Complete(Accepted); e != nil {
		t.Fatal(e)
	}
	if _, e := owner.Issue(context.Background(), DataLedger, 1, pool.Amount{Bytes: 8192, Messages: 2}); e != nil {
		t.Fatal(e)
	}
	urgent, ok := s.TakeFor(Fast, MaxPayload, ControlLedger)
	if !ok || urgent.Record.Kind != Authority {
		t.Fatal(urgent, ok)
	}
	_, message, e := decodeAuthority(urgent.Record.Payload)
	if e != nil || authorityKey(message).Session != current {
		t.Fatal("history delayed fresh permission", message, e)
	}
	if e = urgent.Complete(Accepted); e != nil {
		t.Fatal(e)
	}
	background, ok := s.TakeFor(Fast, MaxPayload, ControlLedger)
	if !ok || background.Record.Kind != Authority {
		t.Fatal("idle recovery lost liveness", background, ok)
	}
	if e = background.Complete(Uncertain); e != nil {
		t.Fatal(e)
	}
	if e = owner.queue(ControlLedger, old); !errors.Is(e, ErrCapacity) {
		t.Fatal("unknown recovery publication was treated as free", e)
	}
	if e = s.Queue(1, []byte("next live request"), time.Now()); e != nil {
		t.Fatal(e)
	}
	next, ok := s.TakeFor(Fast, MaxPayload, AnyLedger)
	if !ok || next.Record.Kind != Data {
		t.Fatal("unknown historical publication blocked independent live DATA", next, ok)
	}
	if len(s.authorityBackground) != maxHistoricalAuthorityQueued {
		t.Fatal("unknown admission was forgotten", s.authorityBackground)
	}
}
