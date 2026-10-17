package tunnel

import (
	"fmt"
	"net"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

func TestRev11MultiRampThroughBriefProviderPause(t *testing.T) {
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	f := newProductionSupervisorFixtureState(t, 10, rev4RetainedState(t))
	var lanes []*lane
	for i := 1; i <= 10; i++ {
		lanes = append(lanes, f.waitLane(t, fmt.Sprintf("a%02d", i), 0))
	}
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 1120, 4096, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	var streams []net.Conn
	for range 12 {
		c := rev4Open(t, f)
		streams = append(streams, c)
		defer c.Close()
	}
	original := rev11StallClientResponses(t, f, lanes, 2400*time.Millisecond)
	transferBudget := 15 * time.Second
	if raceInstrumented {
		transferBudget = 30 * time.Second
	} // Correctness under instrumentation, not a throughput gate.
	errs := make(chan error, len(streams))
	for i, c := range streams {
		go func(c net.Conn, label int) { errs <- rev4Exchange(c, label, 1<<20, transferBudget) }(c, 1121+i)
	}
	if err := rev4Exchange(chat, 1140, 4096, 7*time.Second); err != nil {
		t.Fatal("messaging stream during MULTI ramp", err)
	}
	for range streams {
		if err := <-errs; err != nil {
			snapshot := f.client.Snapshot()
			for _, line := range snapshot.Lines {
				t.Logf("failure_state account=%s ready=%t reason=%s reader_repairs=%d scheduler=%+v", line.Account, line.Ready, line.ReadinessReason, line.ReaderRepairs, line.Scheduler)
			}
			for _, l := range lanes {
				t.Logf("failure_sockets account=%s sockets=%+v", l.account.ID, physicalMail(l.mail).SocketSnapshots())
			}
			t.Fatal("MULTI transfer", err)
		}
	}
	for _, c := range original {
		if !c.Ready() {
			t.Fatal("MULTI ramp replaced live receive socket", c.SocketSnapshot("original"))
		}
	}
	for _, l := range lanes {
		if l.ctx.Err() != nil || physicalMail(l.mail).ReaderRepairs() != 0 {
			t.Fatal("MULTI ramp caused receiver/carrier restart", l.account.ID)
		}
		if err := l.control.Audit(); err != nil {
			t.Fatal(err)
		}
		if err := l.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	fresh := rev4Open(t, f)
	defer fresh.Close()
	if err := rev4Exchange(fresh, 1141, 256<<10, 5*time.Second); err != nil {
		t.Fatal("fresh transfer after MULTI", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := rev4Exchange(chat, 1142, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("original messaging after quiet", err)
	}
	if n := f.client.Snapshot().ActiveLines; n != 20 {
		t.Fatalf("active lines %d/20", n)
	}
	t.Log("accounts=10 active_lines=20 concurrent_tcp_streams=12 verified_mib_each_direction=12 response_stall_ms=2400 receive_reconnects=0 carrier_restarts=0 original_tcp_retained=true fresh_transfer_verified=true")
}

// Block TLS response bytes on existing client sockets without closing them.
// A short queue/retransmission gap must not cause a wave of replacement LOGINs.
func rev11StallClientResponses(t *testing.T, f *productionSupervisorFixture, lanes []*lane, duration time.Duration) []*mailbox.Client {
	t.Helper()
	endpoints := map[string]bool{}
	var readers []*mailbox.Client
	remember := func(c *mailbox.Client) {
		if c != nil && c.Ready() {
			endpoints[c.LocalAddr().String()+"|"+c.RemoteAddr().String()] = true
		}
	}
	for _, l := range lanes {
		m := physicalMail(l.mail)
		readers = append(readers, m.readerRef())
		remember(m.readerRef())
		remember(m.controlClient())
		if c := m.dataReaderRef(); c != nil {
			readers = append(readers, c)
			remember(c)
		}
		m.writerMu.Lock()
		for _, c := range m.writers {
			remember(c)
		}
		m.writerMu.Unlock()
		m.cleanerMu.Lock()
		remember(m.cleaner)
		m.cleanerMu.Unlock()
	}
	until := time.Now().Add(duration).UnixNano()
	f.network.mu.Lock()
	count := 0
	for _, c := range f.network.active {
		if endpoints[c.originalPeer+"|"+c.originalLocal] {
			c.writeStallUntil.Store(until)
			count++
		}
	}
	f.network.mu.Unlock()
	if count != len(endpoints) {
		t.Fatalf("fault covered %d/%d existing client sockets", count, len(endpoints))
	}
	t.Logf("response_stall_ms=%d affected_client_sockets=%d original_receive_sockets=%d", duration.Milliseconds(), count, len(readers))
	return readers
}

func TestRev11BriefProviderSilencePreservesAllReceiveSockets(t *testing.T) {
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	f := newProductionSupervisorFixtureState(t, 10, rev4RetainedState(t))
	var lanes []*lane
	for i := 1; i <= 10; i++ {
		lanes = append(lanes, f.waitLane(t, fmt.Sprintf("a%02d", i), 0))
	}
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 1101, 4096, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	original := rev11StallClientResponses(t, f, lanes, 2400*time.Millisecond)
	for _, l := range lanes {
		if err := l.controlSend(wire.Echo{Token: 11001}); err != nil {
			t.Fatal(err)
		}
		l.armReaderHot(700 * time.Millisecond)
	}
	if err := rev4Exchange(chat, 1102, 4096, 7*time.Second); err != nil {
		t.Fatal("original TCP through provider pause", err)
	}
	time.Sleep(300 * time.Millisecond)
	for _, c := range original {
		if !c.Ready() {
			t.Fatal("brief provider silence destroyed a live receive socket", c.SocketSnapshot("original"))
		}
	}
	for _, l := range lanes {
		m := physicalMail(l.mail)
		if l.ctx.Err() != nil || m.ReaderRepairs() != 0 {
			t.Fatalf("brief silence caused reconnect account=%s repairs=%d", l.account.ID, m.ReaderRepairs())
		}
	}
	fresh := rev4Open(t, f)
	defer fresh.Close()
	if err := rev4Exchange(fresh, 1103, 256<<10, 5*time.Second); err != nil {
		t.Fatal("fresh connection after pause", err)
	}
	if err := rev4Exchange(chat, 1104, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("original TCP after followup", err)
	}
	if n := f.client.Snapshot().ActiveLines; n != 20 {
		t.Fatalf("active lines %d/20", n)
	}
	t.Logf("accounts=10 active_lines=20 response_stall_ms=2400 original_receivers_retained=%d original_tcp_retained=true fresh_transfer_verified=true receive_reconnects=0", len(original))
}

func TestRev11PersistentReceiveBlackholeStillRepairs(t *testing.T) {
	f := newProductionSupervisorFixtureAccounts(t, 2)
	lanes := []*lane{f.waitLane(t, "a01", 0), f.waitLane(t, "a02", 0)}
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 1111, 4096, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	original := rev11StallClientResponses(t, f, lanes, 20*time.Second)
	started := time.Now()
	for _, l := range lanes {
		l.armReaderHot(700 * time.Millisecond)
	}
	deadline := time.Now().Add(13 * time.Second)
	for {
		retired := 0
		for _, c := range original {
			if !c.Ready() {
				retired++
			}
		}
		if retired == len(original) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("persistent blackhole left %d/%d original receivers blocked", len(original)-retired, len(original))
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, l := range lanes {
		if l.ctx.Err() != nil {
			t.Fatal("physical blackhole destroyed logical carrier", l.account.ID)
		}
	}
	// Only receive roles have been required to replace. The control writer still
	// has a healthy TCP connection awaiting the bounded provider response, so end
	// that injected pause before checking application service.
	f.network.mu.Lock()
	for _, c := range f.network.active {
		c.writeStallUntil.Store(0)
	}
	f.network.mu.Unlock()
	// Existing paused Write calls use their original deadline; closing the old
	// control generations models recovery of the outage without replaying APPEND.
	for _, l := range lanes {
		physicalMail(l.mail).controlClient().Close()
	}
	if err := rev4Exchange(chat, 1112, 4096, 10*time.Second); err != nil {
		t.Fatal("original TCP after genuine blackhole", err)
	}
	for _, l := range lanes {
		if l.dataShards > 0 {
			previous := physicalMail(l.mail).previousDataReader.Load()
			if previous == nil || previous.FirstCloseStage != "receive" || previous.FirstCloseReason != "no_progress" {
				t.Fatal("DATA receive first-close cause was lost", l.account.ID, previous)
			}
		}
	}
	t.Logf("persistent_blackhole_receive_retirement_ms=%d original_tcp_retained=true", time.Since(started).Milliseconds())
}
