package tunnel

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
)

type rc8PagedCleanupMail struct {
	*countedCleanupScanMail
	deleteErr error
}

func (m *rc8PagedCleanupMail) Delete(ctx context.Context, row mailbox.Message) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.memoryMail.Delete(ctx, row)
}

func TestRC8UnhintedDataCleanupReclaimsBeforeFetchingMoreBodies(t *testing.T) {
	for _, failDelete := range []bool{false, true} {
		t.Run(map[bool]string{false: "proven", true: "delete-failed"}[failDelete], func(t *testing.T) {
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			s, err := ledger(t.TempDir(), "data", a, u, false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			cipher := cleanupFixtureEpoch(t)
			m := &rc8PagedCleanupMail{countedCleanupScanMail: &countedCleanupScanMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}}
			if failDelete {
				m.deleteErr = errors.New("provider delete failed")
			}
			if err = m.Ensure(context.Background(), u.Down); err != nil {
				t.Fatal(err)
			}
			g, err := s.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: cipher.Context().Session}, pool.Amount{Bytes: 2 << 20, Messages: 20})
			if err != nil {
				t.Fatal(err)
			}
			eligible := map[journal.AttemptRef]bool{}
			var firstPageBytes uint64
			for i := 0; i < 20; i++ {
				body := bytes.Repeat([]byte{byte(i + 1)}, 32<<10)
				raw, err := mailbox.Encode(body)
				if err != nil {
					t.Fatal(err)
				}
				attempt, err := s.PrepareStarted(g.Ref, u.Down, [16]byte{byte(i + 1)}, uint64(len(raw)), body)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Complete(attempt.Ref, journal.Accepted); err != nil {
					t.Fatal(err)
				}
				if err = m.Append(context.Background(), u.Down, raw); err != nil {
					t.Fatal(err)
				}
				// Four eligible bodies on each page; other DATA stays unacknowledged.
				if i < 4 || i >= 16 {
					eligible[attempt.Ref] = true
				}
				if i < 4 {
					firstPageBytes += uint64(len(raw))
				}
			}
			if err = s.CloseGrant(g.Ref); err != nil {
				t.Fatal(err)
			}
			before, err := s.Held(a.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			l := &lane{ctx: context.Background(), account: a, user: u, data: s, mail: m, sendFolder: u.Down, cipher: cipher}
			r := l.cleanup(cleanupLocalData, eligible, nil, nil, true, s)
			if m.pages != 1 || l.outCursor.After != 16 {
				t.Fatal("eligible DATA waited behind another page", m.pages, l.outCursor)
			}
			after, err := s.Held(a.ID, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failDelete {
				if r.err == nil || len(r.local) != 0 || before != after {
					t.Fatal("failed deletion refunded debt", r, before, after)
				}
			} else {
				if r.err != nil || len(r.local) != 4 || after.Bytes != before.Bytes-firstPageBytes || after.Messages != before.Messages-4 {
					t.Fatal("wrong exact refund", r, before, after)
				}
				// The next turn resumes after the first page; unacknowledged DATA
				// must survive both passes even though its BODY was fetched too.
				r = l.cleanup(cleanupLocalData, eligible, nil, nil, true, s)
				if r.err != nil || len(r.local) != 4 {
					t.Fatal("later eligible page starved", r)
				}
				for uid := uint32(5); uid <= 16; uid++ {
					absent, err := m.Absent(l.ctx, u.Down, 1, uid)
					if err != nil || absent {
						t.Fatal("unacknowledged DATA deleted", uid, err)
					}
				}
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
