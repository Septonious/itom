package tunnel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/wire"
)

// Replay the Rev6 lifecycle rather than measuring a new empty tunnel for every
// transfer. All account control sockets disappear together after bulk traffic;
// the existing encrypted carriers and original messaging socket must survive.
func TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier(t *testing.T) {
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	f := newProductionSupervisorFixtureState(t, 15, rev4RetainedState(t))
	chat := rev4Open(t, f)
	bulk := rev4Open(t, f)
	if err := rev4Exchange(bulk, 701, 8<<20, 15*time.Second); err != nil {
		t.Fatal("first bulk", err)
	}
	bulk.Close()
	var lanes []*lane
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("a%02d", i)
		lanes = append(lanes, f.waitLane(t, id, 0))
		f.mu.Lock()
		server := f.serverLanes[id]
		f.mu.Unlock()
		lanes = append(lanes, server)
	}
	for _, l := range lanes {
		physicalMail(l.mail).Client.Close()
	}
	for _, l := range lanes {
		if err := l.controlSend(wire.Echo{Token: 7701}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		replaced := 0
		for _, l := range lanes {
			snapshots := physicalMail(l.mail).SocketSnapshots()
			if snapshots[0].ID != physicalMail(l.mail).Client.SocketSnapshot("old").ID && snapshots[0].State != "dead" {
				replaced++
			}
			if l.ctx.Err() != nil {
				t.Fatalf("socket EOF destroyed carrier account=%s server=%t epoch=%d", l.account.ID, l.side, l.carrierEpoch)
			}
		}
		if replaced == len(lanes) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d physical control sockets repaired", replaced, len(lanes))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := rev4Exchange(chat, 702, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("original chat after EOF", err)
	}
	for round, fanout := range []int{1, 12, 1} {
		errs := make(chan error, fanout)
		for j := 0; j < fanout; j++ {
			c := rev4Open(t, f)
			go func(label int) { defer c.Close(); errs <- rev4Exchange(c, label, 512<<10, 15*time.Second) }(710 + round*20 + j)
		}
		for j := 0; j < fanout; j++ {
			if err := <-errs; err != nil {
				t.Fatal("following transfer", round, err)
			}
		}
		if err := rev4Exchange(chat, 750+round, 4096, 1100*time.Millisecond); err != nil {
			t.Fatal("original chat after repeated transfer", err)
		}
	}
	for _, l := range lanes {
		if l.ctx.Err() != nil {
			t.Fatal("carrier restarted", l.account.ID, l.side)
		}
		if err := l.control.Audit(); err != nil {
			t.Fatal("control accounting", err)
		}
		if err := l.data.Audit(); err != nil {
			t.Fatal("DATA accounting", err)
		}
	}
	t.Log("accounts=15 retained_bytes_per_account=6291456 retained_messages=130 control_eofs=30 carrier_restarts=0 original_chat_retained=true followup_fanout=1,12,1")
}

func TestRev7CappedMixedSocketsRepairWithoutLosingOriginalStream(t *testing.T) {
	if root := os.Getenv("ITOM_LOAD_TRACE_DIR"); root != "" {
		t.Setenv("ITOM_LOAD_TRACE_DIR", filepath.Join(root, fmt.Sprint(time.Now().UnixNano())))
	}
	t.Setenv("ITOM_LOAD_LAYOUT", "mixed")
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "4")
	f := newProductionSupervisorFixtureAccounts(t, 2)
	c := rev4Open(t, f)
	var lanes []*lane
	for _, id := range []string{"a01", "a02"} {
		lanes = append(lanes, f.waitLane(t, id, 0))
		f.mu.Lock()
		lanes = append(lanes, f.serverLanes[id])
		f.mu.Unlock()
	}
	for _, l := range lanes {
		physicalMail(l.mail).Client.Close()
	}
	if err := rev4Exchange(c, 795, 1<<20, 12*time.Second); err != nil {
		rev4RecoveryFailure(t, f)
		for _, l := range lanes {
			l.mu.Lock()
			out := len(l.out)
			l.mu.Unlock()
			t.Logf("capped_repair_account account=%s server=%t out=%d bulk_grants=%d fast_grants=%d control_debt=%d scheduler=%+v sockets=%+v", l.account.ID, l.side, out, len(l.grants), len(l.fastGrants), l.controlDebt.Load(), l.pump.SchedulerSnapshot(), physicalMail(l.mail).SocketSnapshots())
		}
		t.Fatal("limited-socket stream after EOF", err)
	}
	for _, l := range lanes {
		if l.ctx.Err() != nil {
			t.Fatal("mixed base EOF destroyed carrier", l.account.ID, l.side)
		}
	}
	if err := rev4Exchange(c, 796, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("original messaging socket after capped repair", err)
	}
}
