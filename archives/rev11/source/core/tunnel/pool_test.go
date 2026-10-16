package tunnel

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/socks"
	"testing"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestClientAttachmentRotationReplacesExhaustedSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := newStripeGroup(wire.ID{1}, 7)
	laneCtx, laneCancel := context.WithCancel(ctx)
	l := &lane{ctx: laneCtx, cancel: laneCancel, account: servercfg.Account{ID: "a01"}, logicalSession: old.logical, generation: old.generation}
	c := &Client{ctx: ctx, cancel: cancel, stripe: old, logicalSession: old.logical, generation: old.generation, lanes: map[string]*lane{"a01": l}}
	old.active.Store(1)
	var capacity socks.OpenError
	if err := c.rotateAttachment(old); !errors.As(err, &capacity) || capacity.Code != wire.ReasonCapacity {
		t.Fatal("exhaustion must refuse only the new OPEN", err)
	}
	if c.stripe != old || l.ctx.Err() != nil || old.stopped {
		t.Fatal("metadata exhaustion tore down an active logical session")
	}
	old.active.Store(0)
	if err := c.rotateAttachment(old); err != nil {
		t.Fatal(err)
	}
	if c.stripe == nil || c.stripe == old {
		t.Fatal("logical session was not replaced")
	}
	if c.logicalSession == (wire.ID{}) || c.generation == 0 {
		t.Fatal("replacement attachment is invalid")
	}
	if len(c.lanes) != 0 {
		t.Fatal("old lanes remained published after rotation")
	}
	select {
	case <-l.ctx.Done():
	default:
		t.Fatal("old carrier was not canceled for supervisor reconnect")
	}
	if err := c.rotateAttachment(old); err != nil {
		t.Fatal(err)
	}
	if c.stripe == old {
		t.Fatal("stale concurrent rotation restored exhausted stripe")
	}
}

func TestOpenSelectionSharesUnmeasuredHealthyOwners(t *testing.T) {
	now := time.Now()
	var lanes []*lane
	for i := 0; i < 10; i++ {
		l := &lane{account: servercfg.Account{ID: fmt.Sprintf("a%02d", i+1)}}
		l.lastProgress.Store(now.UnixNano())
		lanes = append(lanes, l)
	}
	lanes[1].rtt.Store(250)
	used := map[*lane]bool{}
	for turn := 0; turn < 9; turn++ {
		n := rankOpenLanes(lanes, now)
		l := lanes[turn%n]
		used[l] = true
		l.active.Add(1)
	}
	if len(used) < 3 {
		t.Fatal("unmeasured owners were pinned out", len(used))
	}
	slow := lanes[0]
	slow.rtt.Store(2400)
	n := rankOpenLanes(lanes, now)
	for _, l := range lanes[:n] {
		if l == slow {
			t.Fatal("measured slow lane selected")
		}
	}
}

func TestClientSnapshotUnifiesCurrentReadinessAndCounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	c := &Client{total: 20, lanes: map[string]*lane{}, diagnostics: map[string]*diagnostic{}}
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("a%02d", i)
		c.accountIDs = append(c.accountIDs, id)
		l := &lane{ctx: ctx}
		l.ready.Store(true)
		l.lastProgress.Store(now.UnixNano())
		c.lanes[id] = l
		c.diagnostics[id] = &diagnostic{line: LineDiagnostic{Account: id, Stage: "ready"}}
	}
	c.lanes["a08"].rejoinPending.Store(true)
	c.lanes["a09"].openQuarantineUntil.Store(now.Add(time.Minute).UnixNano())
	c.lanes["a10"].lastProgress.Store(now.Add(-time.Hour).UnixNano())
	check := func(s ClientSnapshot) {
		t.Helper()
		active, total := 0, 0
		for _, l := range s.Lines {
			active += l.ActiveLines
			total += l.TotalLines
			if l.Ready != (l.ActiveLines == 2) {
				t.Fatal("readiness/count disagree", l.Account)
			}
			if l.Account >= "a08" && (l.Ready || l.ReadinessReason == "ready") {
				t.Fatal("historical ready concealed current failure", l.Account)
			}
		}
		if active != 14 || s.ActiveLines != active || total != 20 || s.TotalLines != total {
			t.Fatal("inconsistent UI snapshot", active, total, s.Stats)
		}
	}
	check(c.Snapshot())
	c.diagnostics = nil // current states still exist with file diagnostics disabled
	check(c.Snapshot())
	c.lanes["a08"].rejoinPending.Store(false)
	if c.Snapshot().ActiveLines != 16 {
		t.Fatal("recovery did not refresh ready count")
	}
}
