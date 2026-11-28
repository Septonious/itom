package tunnel

import (
	"fmt"
	"itom.local/mailstream"
	"slices"
	"testing"
	"time"
)

func TestRC23SharedProfileAndSequentialGrant(t *testing.T) {
	for _, n := range []int{3, 10, 11, 15, 20} {
		a := &sessionAdmission{limit: 2, owners: map[string]int{}}
		for i := 1; i <= n; i++ {
			a.accounts = append(a.accounts, fmt.Sprintf("a%02d", i))
		}
		r1, e := a.acquire("first")
		if e != nil {
			t.Fatal(e)
		}
		r2, e := a.acquire("second")
		if e != nil {
			t.Fatal(e)
		}
		if n <= 10 {
			for _, id := range []string{"first", "second"} {
				if !slices.Equal(a.grant(id), a.accounts) {
					t.Fatal(n, id, a.grant(id))
				}
			}
		} else {
			half := (n + 1) / 2
			if !slices.Equal(a.grant("first"), a.accounts[:half]) || !slices.Equal(a.grant("second"), a.accounts[half:]) {
				t.Fatalf("%d: %v", n, a.grants)
			}
		}
		r2()
		r1()
	}
}

func adaptiveSnapshot(a *sessionAdmission, id string, bytes uint64) mailstream.MailSnapshot {
	m := storageSnapshot(a.grant(id), a.accounts)
	for i := range m.Lines {
		l := &m.Lines[i]
		l.PeerActive = l.Allocated
		l.Data = l.Allocated
		l.Writer = l.Allocated
		l.ControlWriter = l.Allocated
		l.IMAPLatencyFresh = true
		l.IMAPLatencyMillis = 40
		l.UploadBytes = bytes
	}
	return m
}
func TestRC23AdaptiveGrantUsesSpareAndDrains(t *testing.T) {
	a, _ := storageFixture(t)
	at := time.Unix(100, 0)
	healthy := mailstream.Snapshot{BulkOutstanding: 256 << 10, InteractiveReceiptRTTMS: 180, InteractiveReceiptMinRTTMS: 160}
	for i := 0; i < 4; i++ {
		healthy.BulkDelivered = uint64(i) * 200000
		a.observeDemand("one", adaptiveSnapshot(a, "one", uint64(i)*200000), healthy, at.Add(time.Duration(2*i)*time.Second))
	}
	if g := a.grant("one"); len(g) != 9 || g[8] != "a09" {
		t.Fatal(g)
	}
	// An unfinished grant cannot start a second grant update.
	m := adaptiveSnapshot(a, "one", 900000)
	m.GrantChanging = true
	if a.observeDemand("one", m, healthy, at.Add(20*time.Second)) {
		t.Fatal("overlapping grant update")
	}
	for i := 0; i < 6; i++ {
		a.observeDemand("one", adaptiveSnapshot(a, "one", 900000), mailstream.Snapshot{BulkDelivered: 600000}, at.Add(time.Duration(20+2*i)*time.Second))
	}
	if len(a.grant("one")) != 8 {
		t.Fatal(a.grant("one"))
	}
}
func TestRC23AdaptiveGrantSharesOnlyIdlePeer(t *testing.T) {
	a, _ := storageFixture(t)
	r, e := a.acquire("two")
	if e != nil {
		t.Fatal(e)
	}
	defer r()
	at := time.Unix(100, 0)
	healthy := mailstream.Snapshot{BulkOutstanding: 256 << 10, InteractiveReceiptRTTMS: 180, InteractiveReceiptMinRTTMS: 160}
	for i := 0; i < 7; i++ {
		now := at.Add(time.Duration(2*i) * time.Second)
		a.observeDemand("two", adaptiveSnapshot(a, "two", uint64(i)*1024), mailstream.Snapshot{}, now)
		healthy.BulkDelivered = uint64(i) * 200000
		a.observeDemand("one", adaptiveSnapshot(a, "one", uint64(i)*200000), healthy, now)
	}
	if len(a.grant("one")) != 9 || len(a.grant("two")) != 7 {
		t.Fatal(a.grants)
	}
	// Peer load returns; its baseline is never revoked. Borrower yields only its extra.
	for i := 7; i < 16; i++ {
		now := at.Add(time.Duration(2*i) * time.Second)
		peer := healthy
		peer.BulkDelivered = uint64(i) * 200000
		a.observeDemand("two", adaptiveSnapshot(a, "two", uint64(i)*200000), peer, now)
		healthy.BulkDelivered = uint64(i) * 200000
		a.observeDemand("one", adaptiveSnapshot(a, "one", uint64(i)*200000), healthy, now)
	}
	if len(a.grant("one")) != 8 || len(a.grant("two")) != 7 {
		t.Fatal(a.grants)
	}
}
func TestRC23AdaptiveGrantDoesNotReactToStaleOrSingleSpike(t *testing.T) {
	a, _ := storageFixture(t)
	at := time.Unix(100, 0)
	s := mailstream.Snapshot{BulkOutstanding: 256 << 10, InteractiveReceiptRTTMS: 180, InteractiveReceiptMinRTTMS: 160}
	for i := 0; i < 5; i++ {
		m := adaptiveSnapshot(a, "one", uint64(i)*200000)
		for j := range m.Lines {
			m.Lines[j].IMAPLatencyFresh = false
		}
		s.BulkDelivered = uint64(i) * 200000
		a.observeDemand("one", m, s, at.Add(time.Duration(2*i)*time.Second))
	}
	if len(a.grant("one")) != 8 {
		t.Fatal(a.grants)
	}
}
func TestRC23UsableAccountsExcludesParkedAndIncompletePaths(t *testing.T) {
	a, _ := storageFixture(t)
	m := adaptiveSnapshot(a, "one", 0)
	m.Lines[0].PeerUnavailable = true
	m.Lines[1].Writer = false
	m.Lines[2].Failure = "IMAP_FAILED"
	if got := usableAccounts(m); got != 5 {
		t.Fatal(got)
	}
}
