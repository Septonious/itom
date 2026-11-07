package mailstream

import (
	"context"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"testing"
	"time"
)

func TestHistoricalDataUsesBoundedHealthyPagesAndConservativeRetries(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "retry"}[retry], func(t *testing.T) {
			limit := int64(16 << 20)
			wantLeft := uint32(64)
			if retry {
				limit = 4 << 20
				wantLeft = 112
			}
			f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}, limit: limit}
			accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			p, err := openMailPipe(ctx, accounts[0])
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			l := &IMAPLink{ctx: ctx, identity: "history-page", attached: SessionID{1}, reclaimer: NewReclamationCoordinator()}
			folder := l.segmentFolder(SessionID{2}, false, 0)
			rec := seedSizedCollection(t, p, folder, 256<<10)
			a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], collectionPaged: retry, spent: rec.bytes, reserved: rec.bytes, recovered: rec.bytes}
			a.maintenance.epoch = l.attached
			a.maintenance.peerPressure = true
			a.maintenance.inventoried = true
			a.maintenance.folders = []string{folder}
			if retry {
				if _, err = p.client.Select(folder, nil).Wait(); err != nil {
					t.Fatal(err)
				}
				if err = p.client.Store(rec.uids, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.maintenanceStep(ctx, p, false)
			if err != nil || got != folder {
				t.Fatalf("bounded historical page failed: %v", err)
			}
			box, err := p.client.Select(folder, nil).Wait()
			if err != nil || box.NumMessages != wantLeft || f.oversized != 0 {
				t.Fatalf("historical page ignored healthy/retry ceiling: remaining=%+v oversized=%d err=%v", box, f.oversized, err)
			}
			if a.spent != rec.bytes || a.recovered != rec.bytes || a.maintenance.folderIndex != 0 {
				t.Fatal("partial historical page refunded unproved debt")
			}
		})
	}
}
