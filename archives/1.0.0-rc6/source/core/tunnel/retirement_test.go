package tunnel

import (
	"context"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

// Delayed retirement ACKs must not amplify every GC status into the full outbox.
func TestCleanupRetirementTrafficIsLinear(t *testing.T) {
	w, e := wallet.Open(filepath.Join(t.TempDir(), "wallet.db"), "12345678", wire.ID{1})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	l := &lane{ctx: context.Background(), wallet: w, remote: map[wire.QuotaKey][]remoteLogicalAttempt{}, retireSent: map[wire.QuotaKey]time.Time{}, controlQueue: make(chan queuedControl, 512)}
	var statuses []wire.CleanupStatus
	for i := uint64(1); i <= 32; i++ {
		k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: i, Session: w.Session(), User: "12345678", Account: "a01"}
		if e = w.Accept(wire.StorageGrant{Key: k, Limit: wire.Quota{Bytes: 10000, Messages: 1}}); e != nil {
			t.Fatal(e)
		}
		usage, _, e := w.Spend(k, "up", []byte("cipher"), func([]byte) (uint64, error) { return 1000, nil })
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Fence(k); e != nil {
			t.Fatal(e)
		}
		status := wire.CleanupStatus{Key: k, Used: usage.Total, Registered: usage.Total, Released: usage.Total, Closed: true}
		statuses = append(statuses, status)
		if e = l.handleCleanupStatus(status); e != nil {
			t.Fatalf("grant %d: %v", i, e)
		}
	}
	for _, s := range statuses {
		if e = l.handleCleanupStatus(s); e != nil {
			t.Fatal(e)
		}
	}
	if n := len(l.controlQueue); n != 32 {
		t.Fatalf("32 grants and duplicate statuses queued %d retirements", n)
	}
	rows, e := w.Retirements()
	if e != nil || len(rows) != 32 {
		t.Fatal(len(rows), e)
	}
	// Lost ACKs still recover on the timer; only the matching grant is repeated.
	for _, s := range statuses {
		l.retireSent[s.Key] = time.Now().Add(-6 * time.Second)
	}
	if e = l.handleCleanupStatus(statuses[0]); e != nil {
		t.Fatal(e)
	}
	if len(l.controlQueue) != 33 {
		t.Fatal("timer replay affected unrelated grants")
	}
	for _, row := range rows {
		row.Ack = true
		if e = w.CompleteRetirement(row); e != nil {
			t.Fatal(e)
		}
	}
	if e = l.handleCleanupStatus(statuses[0]); e != nil || len(l.controlQueue) != 33 {
		t.Fatal("retired status resurrected traffic", e)
	}
}
