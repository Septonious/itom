package tunnel

import("testing";"time")

func TestRC7ManyStreamBatchCollectionLeavesFastTrafficIndependent(t *testing.T) {
  for _,tc:=range []struct{streams int;want time.Duration}{
    {0,20*time.Millisecond},{4,20*time.Millisecond},{15,20*time.Millisecond},
    {16,35*time.Millisecond},{32,35*time.Millisecond},{64,35*time.Millisecond},
  } {
    if got:=rc7BulkCollectionResidence(tc.streams);got!=tc.want{t.Fatalf("streams=%d got=%s want=%s",tc.streams,got,tc.want)}
  }
}
