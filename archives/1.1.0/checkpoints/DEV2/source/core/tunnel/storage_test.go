package tunnel

import (
	"context"
	"crypto/rand"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestRecoveryOnePhysicalUIDProvesOnlyOneIdenticalAttempt(t *testing.T) {
	ctx := context.Background()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	cfg, err := servercfg.New("test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := cfg.AddUser("u")
	if err != nil {
		t.Fatal(err)
	}
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "u", Password: "p"}}
	if err = m.Ensure(ctx, u.Down); err != nil {
		t.Fatal(err)
	}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	session := wire.ID{1}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: session}
	g, err := s.Issue(scope, pool.Amount{Bytes: 8 << 20, Messages: 9})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("identical uncertain append")
	raw, err := mailbox.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		var batch wire.ID
		if _, err = rand.Read(batch[:]); err != nil {
			t.Fatal(err)
		}
		a, prepErr := s.Prepare(g.Ref, u.Down, batch, uint64(len(raw)), body)
		if prepErr != nil {
			t.Fatal(prepErr)
		}
		if _, err = s.Start(a.Ref); err != nil {
			t.Fatal(err)
		}
		if err = s.Complete(a.Ref, journal.Uncertain); err != nil {
			t.Fatal(err)
		}
	}
	// Only one physical copy exists even though nine attempts carry the same digest.
	if err = m.Append(ctx, u.Down, raw); err != nil {
		t.Fatal(err)
	}
	rows, err := inventory(ctx, m, u.Down)
	if err != nil {
		t.Fatal(err)
	}
	gone, err := reclaim(ctx, m, s, rows, func(journal.Attempt) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 {
		t.Fatalf("one physical UID must prove exactly one attempt, removed=%d", len(gone))
	}
	attempts, err := allAttempts(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 8 {
		t.Fatalf("unproved identical attempts were refunded: remaining=%d", len(attempts))
	}
	if absent, err := m.Absent(ctx, u.Down, 1, 1); err != nil || !absent {
		t.Fatalf("physical UID was not deleted once: absent=%v err=%v", absent, err)
	}
}
