package tunnel

import (
 "testing"
 "time"
)

func TestRC8BulkACKFloorCannotRaiseUncalibratedFastRTTBudget(t *testing.T) {
 d:=newDeliveryController()
 d.lowFlightCount=8
 for i:=range d.lowFlightAges {d.lowFlightAges[i]=2*time.Second}
 d.baselineMS=5000
 if d.acknowledgementFloor()!=2*time.Second {t.Fatal("test fixture must have measured slow bulk path")}
 if got:=d.fastAgeBudget();got!=deliveryFastMaxAge {
  t.Fatalf("slow bulk receipts inflated uncalibrated interactive budget: %v",got)
 }
 if d.targetAge()!=2*time.Second {t.Fatal("bulk-specific target unexpectedly modified")}
 d.fastBaselineCount=8
 for i:=range d.fastBaselineAges{d.fastBaselineAges[i]=900*time.Millisecond}
 if got:=d.fastAgeBudget();got!=d.fastPathBaseline()+100*time.Millisecond {
  t.Fatal("isolated unloaded fast baseline lost",got)
 }
 if d.bulk!=0 || d.fast!=0 || d.delivered!=0 {t.Fatal("queue guard invented delivery credit")}
}
