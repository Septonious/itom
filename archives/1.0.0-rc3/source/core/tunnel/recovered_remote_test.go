package tunnel

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type recoveredMail struct {
	*memoryMail
	hiddenUID uint32
	pages     int
}

func (m *recoveredMail) PageRecovery(ctx context.Context, folder string, c mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	m.pages++
	rows, next, err := m.Page(ctx, folder, c)
	kept := rows[:0]
	for _, row := range rows {
		if row.UID != m.hiddenUID {
			kept = append(kept, row)
		}
	}
	return kept, next, err
}

func establishRecoveredServer(t *testing.T, l *lane) {
	t.Helper()
	client, a, err := session.NewGate(session.Client, 1, 0, transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	l.gate, _, err = session.NewGate(session.Server, 1, 0, transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	a, err = l.gate.Receive(1, 0, a.Send[0])
	if err != nil {
		t.Fatal(err)
	}
	a, err = client.Receive(1, 0, a.Send[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.gate.Receive(1, 0, a.Send[0]); err != nil {
		t.Fatal(err)
	}
	l.boot, err = session.NewBootstrap(session.Server, 1, 0, l.data.StoreID(), l.data.Run())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := session.NewBootstrap(session.Client, 1, 0, l.data.StoreID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := l.boot.Poll(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = peer.Receive(1, 0, *offer.Send); err != nil {
		t.Fatal(err)
	}
	ack, err := peer.CommitStore(1, 0, l.data.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.boot.Receive(1, 0, *ack.Send); err != nil {
		t.Fatal(err)
	}
}

// Real persistent journal/wallet restart with an APPEND whose descriptor was
// lost before registration. The live tail has already advanced past that DATA.
// Discovery and descriptor replay must not refund it; physical proof plus the
// peer's final fence must recover space without resetting either durable store.
func TestRestartRecoversUnregisteredOldDATAWithoutQuotaReset(t *testing.T) {
	for _, mode := range []string{"startup_visible", "late_visibility", "later_page", "registered_wallet_acked"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			dir := t.TempDir()
			s, err := ledger(dir, "data", a, u, false)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			wpath := filepath.Join(dir, "wallet.db")
			w, err := wallet.Open(wpath, u.ID, s.StoreID())
			if err != nil {
				t.Fatal(err)
			}
			g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, dataProfile12)
			if err != nil {
				t.Fatal(err)
			}
			offer, err := s.RemoteOffer(g.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if err = w.Accept(offer); err != nil {
				t.Fatal(err)
			}
			report, body, err := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte("old ciphertext"), 1024), func(b []byte) (uint64, error) {
				raw, err := mailbox.Encode(b)
				return uint64(len(raw)), err
			})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "registered_wallet_acked" {
				status, err := s.ApplyRemote(u.ID, s.Run(), report)
				if err != nil {
					t.Fatal(err)
				}
				if err = w.Acknowledge(status); err != nil {
					t.Fatal(err)
				}
			}
			m := &recoveredMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
			if err = m.Ensure(ctx, u.Up); err != nil {
				t.Fatal(err)
			}
			if mode == "later_page" {
				for i := 0; i < 33; i++ {
					raw, _ := mailbox.Encode([]byte("unrelated control"))
					if err = m.Append(ctx, u.Up, raw); err != nil {
						t.Fatal(err)
					}
				}
			}
			raw, err := mailbox.Encode(body)
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Append(ctx, u.Up, raw); err != nil {
				t.Fatal(err)
			}
			uid := m.next[u.Up]
			if mode != "startup_visible" {
				m.hiddenUID = uid
			}
			before, _ := s.Held(a.ID, u.ID)
			oldRun := s.Run()
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = ledger(dir, "data", a, u, false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.Run() == oldRun {
				t.Fatal("fixture did not restart the journal")
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			cipher := cleanupFixtureEpoch(t)
			w, err = wallet.OpenBound(wpath, s.StoreID(), cipher)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			entries, err := w.Entries()
			pending := 1
			if mode == "registered_wallet_acked" {
				pending = 0
			}
			if err != nil || len(entries) != 1 || !entries[0].Fenced || len(entries[0].Pending) != pending || entries[0].Offer.Key.Session == w.Session() {
				t.Fatal("lost restart descriptor/fence", entries, err)
			}
			l := &lane{ctx: ctx, side: true, account: a, user: u, data: s, mail: m, cipher: cipher, recvFolder: u.Up, sendFolder: u.Down,
				pending: map[[32]byte]mailbox.Message{}, reports: map[[32]byte]wire.UsageReport{}, remoteGC: map[journal.AttemptRef]remoteCleanup{},
				issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, controlQueue: make(chan queuedControl, 64), urgentQueue: make(chan queuedControl, 64)}
			rows, err := inventory(ctx, m, u.Up)
			if err != nil {
				t.Fatal(err)
			}
			removed, err := reclaim(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
			if err != nil || len(removed) != 0 {
				t.Fatal("startup refunded unregistered DATA", removed, err)
			}
			if err = l.retainRecoveredRemote(rows, removed); err != nil {
				t.Fatal(err)
			}
			if held, _ := s.Held(a.ID, u.ID); held != before {
				t.Fatal("discovery refunded reservation", held, before)
			}
			establishRecoveredServer(t, l)
			if err = l.queueRegisteredRemoteRecovery(); err != nil {
				t.Fatal(err)
			}
			f, err := wire.EncodeControl(0, report)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "registered_wallet_acked" {
				if err = l.process(f); err != nil {
					t.Fatal(err)
				}
			}
			if len(l.remoteGC) != 1 || len(l.pending) != 0 {
				t.Fatal("old DATA stranded behind live tail", len(l.remoteGC), len(l.pending))
			}
			if held, _ := s.Held(a.ID, u.ID); held != before {
				t.Fatal("descriptor refunded physical debt", held, before)
			}
			if mode == "late_visibility" || mode == "registered_wallet_acked" {
				for i := 0; i < 2; i++ {
					r := l.cleanupRemoteAt(cleanupRemote, nil, l.remoteGC, nil, false, nil, l.remoteRecoveryCursor)
					if r.err != nil || len(r.remote) != 0 {
						t.Fatal("hidden body became deletion proof", r.err, len(r.remote))
					}
					if err = l.finishCleanup(r); err != nil {
						t.Fatal(err)
					}
					if held, _ := s.Held(a.ID, u.ID); held != before {
						t.Fatal("missing UID refunded debt", held, before)
					}
				}
			}
			m.hiddenUID = 0
			m.pages = 0
			for pass := 0; pass < 4 && len(l.remoteGC) > 0; pass++ {
				pages := m.pages
				r := l.cleanupRemoteAt(cleanupRemote, nil, l.remoteGC, nil, false, nil, l.remoteRecoveryCursor)
				if r.err != nil {
					t.Fatal(r.err)
				}
				if m.pages-pages > 1 {
					t.Fatal("recovery monopolized cleaner", m.pages-pages)
				}
				if err = l.finishCleanup(r); err != nil {
					t.Fatal(err)
				}
			}
			status, err := s.RemoteStatus(g.Ref)
			if err != nil || status.Registered != report.Total || status.Released != report.Total || len(l.remoteGC) != 0 {
				t.Fatal("debt did not drain", status, err)
			}
			if gone, _ := m.Absent(ctx, u.Up, 1, uid); !gone {
				t.Fatal("refund preceded physical deletion")
			}
			if held, _ := s.Held(a.ID, u.ID); held != (pool.Amount{Bytes: before.Bytes - report.Total.Bytes, Messages: before.Messages - report.Total.Messages}) {
				t.Fatal("unused allowance refunded before peer fence", held)
			}
			// Delayed duplicate after proof is idempotent, even though its row was
			// removed from the journal. It cannot fail/decrypt in the new epoch.
			if err = l.process(f); err != nil || len(l.remoteGC) != 0 {
				t.Fatal("replayed reclaimed ordinal killed carrier", err)
			}
			if err = l.remoteData(mailbox.Message{Folder: u.Up, Validity: 1, UID: uid, MIME: raw}, report); err != nil || len(l.remoteGC) != 0 {
				t.Fatal("late already-fetched DATA killed carrier", err)
			}
			final, err := w.Fence(offer.Key)
			if err != nil {
				t.Fatal(err)
			}
			ff, err := wire.EncodeControl(0, final)
			if err != nil {
				t.Fatal(err)
			}
			if err = l.process(ff); err != nil {
				t.Fatal(err)
			}
			status, err = s.RemoteStatus(g.Ref)
			if err != nil || !grantFullyReleased(status) {
				t.Fatal("old grant did not close", status, err)
			}
			if len(l.issued) < 3 {
				t.Fatal("fresh DATA grants still starved", len(l.issued))
			}
			retire, err := wire.EncodeControl(0, wire.Retirement{Key: offer.Key, Final: final.Final})
			if err != nil {
				t.Fatal(err)
			}
			if err = l.process(retire); err != nil {
				t.Fatal(err)
			}
			if err = l.remoteData(mailbox.Message{Folder: u.Up, Validity: 1, UID: uid, MIME: raw}, report); err != nil || len(l.remoteGC) != 0 {
				t.Fatal("late DATA for retired grant killed carrier", err)
			}
			for key := range l.issued {
				if key.Session != cipher.Context().Session || key.Epoch != s.Run() {
					t.Fatal("old lease became spendable", key)
				}
			}
			if _, _, err = w.Spend(offer.Key, u.Up, []byte("retry"), mailboxEncodedCost); !errors.Is(err, wallet.ErrState) {
				t.Fatal("old wallet resumed writes", err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func mailboxEncodedCost(body []byte) (uint64, error) {
	raw, err := mailbox.Encode(body)
	return uint64(len(raw)), err
}
