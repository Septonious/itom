package tunnel

import (
	"testing"
	"time"
)

func TestRC7AsymmetricWindowServer16Client12(t *testing.T) {
	for _, tc := range []struct{server bool; want uint64}{{true,16<<20},{false,12<<20}} {
		d := newDeliveryController()
		d.setRoleWindow(tc.server)
		if d.maxWindow != tc.want { t.Fatalf("side=%t: bound=%d want=%d",tc.server,d.maxWindow,tc.want) }
		d.window = tc.want
		next := d.policy.next(time.Now(), deliveryFeedback{
			Window:d.window, MaxWindow:d.maxWindow, Flight: d.maxWindow/2, ACKFlight:d.maxWindow/2,
			Delivered:123, Age:120*time.Millisecond, FastAge:100*time.Millisecond,
			Target:800*time.Millisecond, FastTarget:500*time.Millisecond,
			Samples:3, Demand:true,
		})
		if next > tc.want { t.Fatalf("side=%t: policy exceeded limit=%d",tc.server,next) }
		d.window = 20<<20
		d.setRoleWindow(tc.server)
		if d.window != tc.want {t.Fatalf("side=%t: oversized window not clamped=%d",tc.server,d.window)}
	}
}

func TestRC7DirectionalLogicalFlightLeavesPhysicalDebtUntouched(t *testing.T) {
	d:=newDeliveryController()
	d.setRoleWindow(false)
	if d.maxWindow != 12<<20 || deliveryMaxWindow != 16<<20 {t.Fatal("asymmetric bounds changed")}
	if d.bulk!=0 || d.fast!=0 || len(d.ranges)!=0 {t.Fatal("role selection unexpectedly changed logical debt")}
}
