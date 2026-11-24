package mailstream

import (
	"context"
	imap "github.com/emersion/go-imap/v2"
	"testing"
	"time"
)

// A live control failure can precede historical-pressure recovery. The frozen
// startup watermark can already be covered while the failed live prefix is
// higher. Recovery must resolve that quarantine before it can LIST old DATA.
func TestRecoveryResolvesControlQuarantineAboveFrozenStartupFence(t *testing.T) {
	for _, outgoing := range []bool{false, true} {
		t.Run(map[bool]string{false: "incoming", true: "outgoing"}[outgoing], func(t *testing.T) {
			accounts, _ := applicationMailServer(t, true, false)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			p, err := openMailPipe(ctx, accounts[0])
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			c := NewReclamationCoordinator()
			l := &IMAPLink{ctx: ctx, identity: "retry-before-history", attached: SessionID{1}, reclaimer: c}
			var records [2]*segmentRecord
			for n, out := range []bool{false, true} {
				folder := l.controlFolder(out)
				records[n] = seedCollectionFolder(t, p, folder, 128)
				if _, err = p.client.Select(folder, nil).Wait(); err != nil {
					t.Fatal(err)
				}
				old := imap.UIDSet{{Start: 1, Stop: 64}}
				if err = p.client.Store(old, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
					t.Fatal(err)
				}
				if err = p.client.Expunge().Close(); err != nil {
					t.Fatal(err)
				}
			}
			reader := &mailPipe{validity: records[0].validity}
			reader.cursor.Store(128)
			a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], control: reader, collectionPaged: true}
			m := &a.maintenance
			m.epoch = l.attached
			m.peerPressure = true
			m.remoteStartOut = uidWatermark{records[0].validity, 64}
			m.clearedIn = m.remoteStartOut
			m.remoteRead = uidWatermark{records[1].validity, 128}
			m.initialOut = uidWatermark{records[1].validity, 64}
			m.clearedOut = m.initialOut
			folder := l.controlFolder(outgoing)
			c.folder(folder)
			c.stage("expunge")
			c.pause("MAINTENANCE_TIMEOUT", a)
			c.mu.Lock()
			c.quarantines[reclamationAccountKey(a)].retryAt = time.Time{}
			c.mu.Unlock()
			got, err := a.maintenanceStep(ctx, p, false)
			if err != nil || got != folder {
				t.Fatalf("pending control quarantine was skipped during historical recovery: folder=%q err=%v", got, err)
			}
			c.completed(a, got)
			if c.snapshot().Paused {
				t.Fatal("audited control retry did not release quarantine")
			}
			if _, err = a.maintenanceStep(ctx, p, false); err != nil {
				t.Fatal(err)
			}
			if !m.inventoried {
				t.Fatal("historical recovery never reached folder inventory")
			}
			if !outgoing && m.clearedIn.UID != 96 || outgoing && m.clearedOut.UID != 96 {
				t.Fatal("retry did not prove its bounded32-message live prefix")
			}
			// The uncollected remainder stays present; completing one retry cannot
			// manufacture an empty-folder proof or refund unrelated old DATA.
			box, err := p.client.Select(folder, nil).Wait()
			if err != nil || box.NumMessages != 32 {
				t.Fatalf("unproved tail changed: box=%+v err=%v", box, err)
			}
		})
	}
}
