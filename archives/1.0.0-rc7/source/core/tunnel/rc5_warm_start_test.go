package tunnel

import (
 "testing"
 "time"
)

// The first cold or repaired DATA path never learns speed from APPEND alone.
// Only the same logical carrier's recent authenticated BODY history supports
// a bounded warm probe after a short idle.
func TestRC5BoundedWarmBodyResume(t *testing.T) {
 now := time.Now()
 cases := []struct {
  name string
  age time.Duration
  samples uint64
  slow uint32
  target uint64
  want uint64
 }{
  {"cold",0,0,0,0,32<<10},
  {"recent_healthy",40*time.Second,12,0,0,128<<10},
  {"too_few_receipts",40*time.Second,7,0,0,32<<10},
  {"previously_slow",40*time.Second,12,1,0,32<<10},
  {"still_restricted",40*time.Second,12,0,64<<10,32<<10},
  {"old_history",100*time.Second,12,0,0,32<<10},
 }
 for _, tc := range cases {
  t.Run(tc.name,func(t *testing.T) {
   l := &lane{side:true,peerReadSamples:tc.samples,
    peerReadSlowCount:tc.slow,peerReadBatchTarget:tc.target}
   if tc.age>0 {l.peerReadAt=now.Add(-tc.age)}
   if got:=l.serviceBatchTarget();got!=tc.want {
    t.Fatalf("batch after idle = %d, want %d",got,tc.want)
   }
  })
 }
}
