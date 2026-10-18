package tunnel

import (
    "testing"
    "time"

    "itom.local/core/wire"
)

// Queue-inflated RTT is a placement warning, not an exclusion or permission
// to refund physical APPEND/journal debt.
func TestRC8FastSpikePenaltyIsBoundedAndFresh(t *testing.T) {
    cases := []struct {age,current,smooth int64; expected int}{
        {0,180,300,0},
        {0,230,200,0},
        {0,290,200,100},
        {0,1000,200,240},
        {10001,1000,200,0},
        {-1,1000,200,0},
        {0,1000,0,0},
    }
    for _, tc := range cases {
        got := fastLoadedRTTSpikePenalty(tc.age,tc.current,tc.smooth)
        if got != tc.expected {t.Fatalf("age=%d current=%d smooth=%d got=%d want=%d",tc.age,tc.current,tc.smooth,got,tc.expected)}
    }
}

func TestRC8ShortSelectionAvoidsFreshRTTSpikeWithoutExcludingLane(t *testing.T) {
    g := newStripeGroup(wire.ID{1},1)
    a := stripeTestLane(g,"a01",false)
    b := stripeTestLane(g,"a02",false)
    defer a.cancel()
    defer b.cancel()
    if err:=g.attach(a);err!=nil{t.Fatal(err)}
    if err:=g.attach(b);err!=nil{t.Fatal(err)}
    now:=time.Now().UnixMilli()
    for _,l:=range []*lane{a,b}{
        l.idleRTT.Store(200)
        l.idleRTTAt.Store(now)
        l.smoothedRTT.Store(200)
        l.rttAt.Store(now)
    }
    a.rtt.Store(650)
    b.rtt.Store(210)
    g.mu.Lock()
    keys:=[]string{"a01","a02"}
    g.prioritizeFastTargetsLocked(keys,nil)
    g.mu.Unlock()
    if keys[0]!="a02"{t.Fatalf("short request selected freshly spiking lane: %v",keys)}
    if len(keys)!=2 {t.Fatal("unstable RTT removed a usable account")}
}
