package tunnel

import (
	"fmt"
	mailstream "itom.local/mailstream"
	"slices"
	"testing"
	"time"
)

func storageFixture(t *testing.T) (*sessionAdmission, mailstream.MailSnapshot) {
	t.Helper()
	a := &sessionAdmission{limit: 3, owners: map[string]int{}}
	for i := 1; i <= 15; i++ {
		a.accounts = append(a.accounts, fmt.Sprintf("a%02d", i))
	}
	release, e := a.acquire("one")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(release)
	return a, storageSnapshot(a.grant("one"), a.accounts)
}
func storageSnapshot(grant, accounts []string) mailstream.MailSnapshot {
	m := mailstream.MailSnapshot{GrantNegotiated: true, AllocatedAccounts: len(grant), Accounts: len(accounts)}
	for _, id := range accounts {
		m.Lines = append(m.Lines, mailstream.MailAccountSnapshot{Account: id, Ready: true, Allocated: slices.Contains(grant, id), BulkHeadroomBytes: 48 << 20})
	}
	return m
}
func TestRC5StorageReplacementRetainsGrantSize(t *testing.T) {
	a, m := storageFixture(t)
	start := time.Unix(1000, 0)
	old := a.grant("one")
	m.Lines[0].BulkHeadroomBytes = 100
	m.Lines[0].BulkBlocked = true
	m.Lines[8].BulkHeadroomBytes = 55 << 20
	m.Lines[9].BulkHeadroomBytes = 60 << 20
	m.Lines[9].PeerStoragePressure = true
	for i := 0; i < 2; i++ {
		if a.observeStorage("one", m, start.Add(time.Duration(i)*2*time.Second)) {
			t.Fatal("transient pressure rotated grant")
		}
	}
	if !a.observeStorage("one", m, start.Add(4*time.Second)) {
		t.Fatal("persistent pressure not resolved")
	}
	got := a.grant("one")
	if len(got) != 8 || slices.Contains(got, old[0]) || !slices.Contains(got, "a09") {
		t.Fatalf("unexpected replacement %v", got)
	}
	if a.observeStorage("one", m, start.Add(time.Minute)) {
		t.Fatal("unacknowledged old grant changed again")
	}
	m = storageSnapshot(got, a.accounts)
	m.Lines[1].BulkHeadroomBytes = 0
	for i := 0; i < 3; i++ {
		if a.observeStorage("one", m, start.Add(time.Duration(6+2*i)*time.Second)) {
			t.Fatal("cooldown ignored")
		}
	}
}
func TestRC5StorageReplacementNeverStealsOtherGrant(t *testing.T) {
	a, m := storageFixture(t)
	release, e := a.acquire("two")
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	m = storageSnapshot(a.grant("one"), a.accounts)
	m.Lines[0].BulkHeadroomBytes = 0
	old := a.grant("one")
	other := a.grant("two")
	for i := 0; i < 5; i++ {
		if a.observeStorage("one", m, time.Unix(1000+int64(i)*2, 0)) {
			t.Fatal("stole occupied account")
		}
	}
	if !slices.Equal(old, a.grant("one")) || !slices.Equal(other, a.grant("two")) {
		t.Fatal("grant changed")
	}
}
func TestRC5HealthyRolloverDoesNotRotate(t *testing.T) {
	a, m := storageFixture(t)
	m.Lines[0].BulkBlocked = true
	for i := 0; i < 5; i++ {
		if a.observeStorage("one", m, time.Unix(1000+int64(i)*2, 0)) {
			t.Fatal("normal DATA rollover caused grant change")
		}
	}
	m.GrantChanging = true
	m.Lines[0].BulkHeadroomBytes = 0
	for i := 0; i < 5; i++ {
		if a.observeStorage("one", m, time.Unix(1100+int64(i)*2, 0)) {
			t.Fatal("concurrent grant transition")
		}
	}
}

func TestRC7AvailabilityReplacesBothFailedAccounts(t *testing.T) {
	a, m := storageFixture(t)
	old := a.grant("one")
	// Field state: one CREATE stall, one dead reader, both with almost full headroom.
	m.Lines[0].Ready = false
	m.Lines[0].Failure = "MAIL_ACCOUNT_UNAVAILABLE"
	m.Lines[4].Ready = false
	m.Lines[4].Failure = "MAIL_CARRIER_CLOSED"
	m.Lines[8].BulkHeadroomBytes = 60 << 20
	m.Lines[9].BulkHeadroomBytes = 59 << 20
	for i := 0; i < 2; i++ {
		if a.observeStorage("one", m, time.Unix(1000+int64(i)*2, 0)) {
			t.Fatal("premature replacement")
		}
	}
	if !a.observeStorage("one", m, time.Unix(1004, 0)) {
		t.Fatal("missing accounts were ignored")
	}
	got := a.grant("one")
	if len(got) != 8 || slices.Contains(got, old[0]) || slices.Contains(got, old[4]) || !slices.Contains(got, "a09") || !slices.Contains(got, "a10") {
		t.Fatal(got)
	}
}
func TestRC7PeerFailureReplacedButHealthyParkedAccountRetained(t *testing.T) {
	a, m := storageFixture(t)
	old := a.grant("one")
	m.Lines[0].PeerUnavailable = true
	for i := 0; i < 3; i++ {
		a.observeStorage("one", m, time.Unix(1000+int64(i)*2, 0))
	}
	got := a.grant("one")
	if len(got) != 8 || slices.Contains(got, old[0]) || !slices.Contains(got, old[1]) {
		t.Fatal(got)
	}
}
func TestRC7InitialGrantPrefersReadyAccounts(t *testing.T) {
	_, m := storageFixture(t)
	m.Lines[0].Ready = false
	m.Lines[0].BulkHeadroomBytes = 66 << 20
	m.Lines[1].Ready = false
	m.Lines[1].BulkHeadroomBytes = 65 << 20
	got := preferredGrantAccounts(m.Lines)
	if slices.Contains(got[:8], "a01") || slices.Contains(got[:8], "a02") {
		t.Fatal(got)
	}
}

func TestRC7RepairUnacknowledgedGrant(t *testing.T) {
	a, m := storageFixture(t)
	old := a.grant("one")
	m.Lines[0].Ready = false
	for i := 0; i < 3; i++ {
		a.observeStorage("one", m, time.Unix(1000+int64(i)*2, 0))
	}
	candidate := a.grant("one")
	if slices.Equal(old, candidate) {
		t.Fatal("no initial replacement")
	}
	m.GrantChanging = true
	for i := range m.Lines {
		if m.Lines[i].Account == candidate[0] {
			m.Lines[i].PeerUnavailable = true
		}
	}
	for i := 0; i < 3; i++ {
		a.observeStorage("one", m, time.Unix(1010+int64(i)*2, 0))
	}
	got := a.grant("one")
	if len(got) != 8 || slices.Contains(got, candidate[0]) || slices.Contains(got, old[0]) {
		t.Fatal(got)
	}
}
