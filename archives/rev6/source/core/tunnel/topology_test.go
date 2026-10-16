package tunnel

import (
	"bytes"
	"io"
	"itom.local/core/servercfg"
	"testing"
	"time"
)

func TestTopologyCapabilitiesBindAvailableService(t *testing.T) {
	for _, n := range []int{0, 1, 2, 4} {
		c := uint64(transportCapabilities)
		if n > 0 {
			c |= capSplitData
		}
		if n > 1 {
			c |= capTwoDataShards
		}
		if n > 2 {
			c |= capFourDataShards
		}
		if !validCapabilities(c) || shardCount(c) != n {
			t.Fatal(n, c)
		}
		for _, m := range []int{0, 1, 2, 4} {
			d := uint64(transportCapabilities)
			if m > 0 {
				d |= capSplitData
			}
			if m > 1 {
				d |= capTwoDataShards
			}
			if m > 2 {
				d |= capFourDataShards
			}
			if shardCount(intersectCapabilities(c, d)) != min(n, m) {
				t.Fatal(n, m)
			}
		}
	}
	for _, c := range []uint64{0, transportCapabilities ^ capDataBatch, transportCapabilities | capFourDataShards, transportCapabilities | 1<<63} {
		if validCapabilities(c) {
			t.Fatal("accepted invalid flags", c)
		}
	}
}
func TestTopologyFolderCreditIndependent(t *testing.T) {
	u := servercfg.User{Up: "itom-up", Down: "itom-down"}
	for _, n := range []int{0, 1, 2, 4} {
		folders := topologyFolders(u, n)
		if len(folders) != 2+2*n {
			t.Fatal(n)
		}
		l := &lane{sendFolder: u.Up, dataShards: n}
		seen := map[string]bool{}
		for i := 0; i < max(1, n)*2; i++ {
			seen[l.nextDataFolder()] = true
		}
		if len(seen) != max(1, n) {
			t.Fatal(seen)
		}
	}
}
func TestConfiguredElevenAccountPublicTransport(t *testing.T) {
	f := newProductionSupervisorFixtureAccounts(t, 11)
	p := f.openOn(t, "a11")
	defer p.Close()
	if err := supervisorEcho(p, 77); err != nil {
		t.Fatal(err)
	}
	if len(f.client.accountIDs) != 11 {
		t.Fatal("account ceiling remains")
	}
}

func TestSplitFastSlotDoesNotWaitForBulkWriter(t *testing.T) {
	l := &lane{dataShards: 1, physicalSlots: make(chan struct{}, 1), fastPhysicalSlots: make(chan struct{}, 1)}
	if !tryAcquireClassSlot(l, false) || !tryAcquireClassSlot(l, true) {
		t.Fatal("service slots coupled")
	}
	if tryAcquireClassSlot(l, false) || tryAcquireClassSlot(l, true) {
		t.Fatal("unbounded service slots")
	}
	stripeLease{lane: l, fast: true}.release()
	if len(l.physicalSlots) != 1 || len(l.fastPhysicalSlots) != 0 {
		t.Fatal("fast release freed bulk")
	}
	stripeLease{lane: l}.release()
	if len(l.physicalSlots) != 0 {
		t.Fatal("bulk release")
	}
}

func TestLowSessionBudgetPublicTransportHasNoDataBlackhole(t *testing.T) {
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "2")
	t.Setenv("ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", "2")
	f := newProductionSupervisorFixture(t)
	p := f.openOn(t, "a01")
	p.SetDeadline(time.Now().Add(20 * time.Second))
	payload := bytes.Repeat([]byte("low-session-budget/"), 16000)
	done := make(chan error, 1)
	go func() { done <- productionWriteFull(p, payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(p, got); err != nil {
		t.Fatal("base DATA fallback", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, got) {
		t.Fatal("fallback DATA corrupted")
	}
	f.client.mu.Lock()
	l := f.client.lanes["a01"]
	f.client.mu.Unlock()
	if l.dataShards != 0 {
		t.Fatal("advertised nonexistent split service")
	}
}
