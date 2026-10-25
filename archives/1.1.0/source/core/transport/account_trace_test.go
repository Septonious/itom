package transport_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/transport"
	"itom.local/core/wallet"
)

// Bounded diagnostics cover the COMPLETE request, including response body reads.
// Reporting happens after the measured request; it changes no request deadline.
func traceAccountRequest(t *testing.T, ctx context.Context, p *accountRosterPair, a, b *transport.Session, client, server *transport.RosterService, stream uint64) (stop func(), report func()) {
	t.Helper()
	started := time.Now()
	requestNS := p.journals[0].Control.CommitTimeline().SampledAtNS
	services := []*transport.RosterService{client, server}
	before := []transport.RuntimeSnapshot{client.Runtime.Snapshot(), server.Runtime.Snapshot()}
	traceCtx, cancel := context.WithCancel(ctx)
	var tracing sync.WaitGroup
	var samples []string
	tracing.Go(func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-traceCtx.Done():
				return
			case scheduled := <-tick.C:
				observed := time.Now()
				left, _ := a.FlowSnapshot(stream)
				clientSampled := time.Now()
				right, _ := b.FlowSnapshot(stream)
				serverSampled := time.Now()
				if len(samples) < 50 {
					samples = append(samples, fmt.Sprintf("%s tick_lag=%s client_actor_wait=%s server_actor_wait=%s client=%+v server=%+v", time.Since(started), observed.Sub(scheduled), clientSampled.Sub(observed), serverSampled.Sub(clientSampled), left, right))
				}
			}
		}
	})
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); tracing.Wait() }) }
	report = func() {
		stop()
		t.Logf("request trace stream=%d started_ns=%d", stream, requestNS)
		for _, sample := range samples {
			t.Log(sample)
		}
		for side, svc := range services {
			after := svc.Runtime.Snapshot()
			for n, c := range after.Carriers {
				previous := before[side].Carriers[n]
				prepared := c.Publish.PrepareTime - previous.Publish.PrepareTime
				published := c.Publish.PublishTime - previous.Publish.PublishTime
				received := c.Receive.ServiceTime - previous.Receive.ServiceTime
				if prepared > 0 || published > 0 || c.Receive.Applied > previous.Receive.Applied {
					t.Logf("side=%d account=%s plane=%d prepare=%s publish=%s receive=%s applied=%d blocked_data=%t blocked_control=%t", side, c.Account, c.Plane, prepared, published, received, c.Receive.Applied-previous.Receive.Applied, c.Publish.DataBlocked, c.Publish.ControlBlocked)
				}
			}
		}
		for _, j := range p.journals {

			wallets, _ := p.client.Wallets(j.Account)
			for class, w := range []*wallet.Wallet{wallets.Control, wallets.Data} {
				timeline := w.CommitTimeline()
				var overlap []wallet.CommitEvent
				for _, event := range timeline.Slowest {
					if event.FinishedNS >= requestNS {
						overlap = append(overlap, event)
					}
				}
				if len(overlap) > 0 || timeline.ActiveSinceNS > 0 {
					t.Logf("account=%s wallet_ledger_index=%d request_started_ns=%d active_ns=%d overlapping_slow_updates=%+v", j.Account, class, requestNS, timeline.ActiveSinceNS, overlap)
				}
			}
			for class, store := range []*journal.Store{j.Control, j.Data} {
				timeline := store.CommitTimeline()
				var overlap []journal.CommitEvent
				for _, event := range timeline.Slowest {
					if event.FinishedNS >= requestNS {
						overlap = append(overlap, event)
					}
				}
				if len(overlap) > 0 || timeline.ActiveSinceNS > 0 {
					t.Logf("account=%s ledger_index=%d request_started_ns=%d active_ns=%d overlapping_slow_updates=%+v", j.Account, class, requestNS, timeline.ActiveSinceNS, overlap)
				}
			}
		}
	}
	return stop, report
}
