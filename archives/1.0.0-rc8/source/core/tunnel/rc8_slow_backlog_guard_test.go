package tunnel

import (
	"testing"
	"time"
)

func TestRC8ConstrainedLinkBulkGuardKeepsFastBudget(t *testing.T) {
  cases:=[]struct{
    rate float64
    age,fastAge time.Duration
    wantCap uint64
    active bool
  }{
    {2.3,3*time.Second,1500*time.Millisecond,128<<10,true},
    {10,900*time.Millisecond,800*time.Millisecond,400000,true},
    {20,900*time.Millisecond,800*time.Millisecond,512<<10,true},
    {40,2*time.Second,2*time.Second,0,false},
    {5,300*time.Millisecond,300*time.Millisecond,0,false},
    {20,200*time.Millisecond,800*time.Millisecond,512<<10,true},
    {0,900*time.Millisecond,900*time.Millisecond,0,false},
  }
  for _,tc:=range cases{
    b,ok:=rc8SlowBacklogGuard(deliveryFeedback{Demand:true,Flight:1<<20,RateMbps:tc.rate,Age:tc.age,FastAge:tc.fastAge})
    if ok!=tc.active || b!=tc.wantCap{t.Errorf("rate %.2f age=%v fast=%v got %d,%t want %d,%t",tc.rate,tc.age,tc.fastAge,b,ok,tc.wantCap,tc.active)}
  }
  b,ok:=rc8SlowBacklogGuard(deliveryFeedback{Demand:true,RateMbps:5,Age:time.Second})
  if ok||b!=0{t.Fatal("idle state invented control/admission debt")}
}
