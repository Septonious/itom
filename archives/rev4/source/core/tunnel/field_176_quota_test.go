package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// Rev9 field traces: a01/a02 reach ready; the remaining accounts authenticate
// but retain 159-176 of 192 DATA message slots and never publish a fast grant.
var fieldRetainedMessages = []uint64{85, 85, 176, 168, 170, 159, 159, 173, 162, 172}

func TestField176RetainedMessagesPreserveDebtAndBootstrap(t *testing.T) {
	for i, messages := range append(append([]uint64(nil), fieldRetainedMessages...), 178, 179, 180, 192) {
		t.Run(fmt.Sprintf("a%02d", i+1), func(t *testing.T) {
			a := servercfg.Account{ID: fmt.Sprintf("a%02d", i+1)}
			a.Mail.Host = "mail.example"
			u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
			s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: messages})
			if err != nil {
				t.Fatal(err)
			}
			l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
			establishRecoveredServer(t, l)
			if err = l.offer(); err != nil {
				t.Fatal(err)
			}
			fast, bulk := l.issuedGrantCounts()
			expectedReady := messages <= 178
			if l.ready.Load() != expectedReady || (expectedReady && (fast != 1 || bulk < 1)) || (!expectedReady && (fast != 0 || bulk != 0)) {
				t.Fatalf("authenticated account stranded: old_messages=%d ready=%t fast=%d bulk=%d", messages, l.ready.Load(), fast, bulk)
			}
			unchanged, err := s.GetGrant(old.Ref)
			if err != nil || unchanged != old {
				t.Fatal("historical reservation changed without proof", err)
			}
			held, err := s.Held(a.ID, u.ID)
			reserveMessages := max(uint64(2), dataProfile12.Messages/16)
			if err != nil || expectedReady && held.Messages > dataProfile12.Messages-reserveMessages || held.Bytes > dataProfile12.Bytes-localFastReserveBytes-dataBatchMIMEHardCap {
				t.Fatal("local capacity reserve consumed", held, err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestField176RetainedMessagesTLSAllAccounts(t *testing.T) {
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "0")
	oldGrants := map[string]journal.Grant{}
	f := newProductionSupervisorFixtureState(t, 10, func(serverDir, _ string, c servercfg.Config, u servercfg.User) {
		for i, a := range c.Accounts {
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: fieldRetainedMessages[i]})
			if err != nil {
				t.Fatal(err)
			}
			oldGrants[a.ID] = g
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("a%02d", i)
		p := f.openOn(t, id)
		for n := 0; n < 4; n++ {
			if err := supervisorEcho(p, i*10+n); err != nil {
				t.Fatalf("%s data did not progress: %v", id, err)
			}
		}
		f.mu.Lock()
		server := f.serverLanes[id]
		f.mu.Unlock()
		g, err := server.data.GetGrant(oldGrants[id].Ref)
		if err != nil || g != oldGrants[id] {
			t.Fatal("old debt was forgiven during traffic", id, err)
		}
		if err = server.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	s := f.client.Snapshot()
	if s.ActiveLines != 20 {
		t.Fatalf("only %d/20 transport directions", s.ActiveLines)
	}
	t.Log("field retained message counts reproduced; 10/10 accounts ready, 20/20 directions; TCP echo on every account; old debt unchanged")
}
