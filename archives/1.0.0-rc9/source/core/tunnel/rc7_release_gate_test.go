package tunnel

import (
  "math"
  "testing"
  "time"
)

const rc7MinimumDuplexMbps=75.0
const rc7MaximumLoadedRTT=750*time.Millisecond

// RC7 speed numbers refer to an unconstrained >=150Mbps synthetic provider.
// The verified rate also includes sustained 30s and final-10s tails, excluding
// a transient peak that might mask an eventual stalled direction.
func rc7ReleasePerformanceGate(upload,download,verified float64,rtt time.Duration,minimum float64) bool {
  if math.IsNaN(minimum) || math.IsInf(minimum,0) || minimum<rc7MinimumDuplexMbps { return false }
  return strictSpeedGate(upload,minimum) && strictSpeedGate(download,minimum) &&
    strictSpeedGate(verified,minimum) && rtt>0 && rtt<=rc7MaximumLoadedRTT
}

func TestRC7NoThroughputWaiverAndStrictRTT(t *testing.T) {
 cases:=[]struct{up,down,verified float64;rtt time.Duration;want bool}{
   {75,75,75,750*time.Millisecond,true},
   {90,78,76,600*time.Millisecond,true},
   {74.999,200,74.999,300*time.Millisecond,false},
   {125,74.999,74.999,300*time.Millisecond,false},
   {100,100,74.999,400*time.Millisecond,false},
   {100,100,100,750*time.Millisecond+time.Nanosecond,false},
   {100,100,100,0,false},
   {math.NaN(),90,90,600*time.Millisecond,false},
 }
 for _,tc:=range cases {
   if got:=rc7ReleasePerformanceGate(tc.up,tc.down,tc.verified,tc.rtt,75); got!=tc.want{
      t.Fatalf("RC7 gate up=%.4f down=%.4f tail=%.4f RTT=%v got=%v want=%v",tc.up,tc.down,tc.verified,tc.rtt,got,tc.want)
   }
 }
 for _,min:=range []float64{0,74.999,math.NaN(),math.Inf(1)} {
   if rc7ReleasePerformanceGate(100,100,100,500*time.Millisecond,min) {
      t.Fatalf("RC7 let release lower its floor to %g",min)
   }
 }
}
