package tunnel

import (
	"testing"
	"time"

	"itom.local/core/wire"
)

func TestRC8FastControlFeedbackEscapesFreshRTTSpike(t *testing.T) {
	g:=newStripeGroup(wire.ID{1},1)
	for _,id:=range []string{"a01","a02"} {
		l:=stripeTestLane(g,id,false)
		defer l.cancel()
		if err:=g.attach(l);err!=nil {t.Fatal(err)}
		l.idleRTT.Store(200)
		l.idleRTTAt.Store(time.Now().UnixMilli())
		l.smoothedRTT.Store(200)
		l.rttAt.Store(time.Now().UnixMilli())
	}
	slow:=g.lanes["a01"]
	fast:=g.lanes["a02"]
	slow.rtt.Store(800)
	fast.rtt.Store(220)
	g.mu.Lock()
	slowCost,fastCost:=g.rc8FastControlScoreLocked(slow),g.rc8FastControlScoreLocked(fast)
	g.mu.Unlock()
	if slowCost<=fastCost {t.Fatalf("fresh RTT spike did not move nonbulk control work: a01=%d a02=%d",slowCost,fastCost)}
	slow.rttAt.Store(time.Now().Add(-12*time.Second).UnixMilli())
	g.mu.Lock()
	stale:=g.rc8FastControlScoreLocked(slow)
	g.mu.Unlock()
	if stale>=slowCost {t.Fatalf("stale RTT spike continued penalizing control: fresh=%d stale=%d",slowCost,stale)}
}
