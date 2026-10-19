package tunnel

import (
	"testing"
	"time"
)

// A diagnostic measurement is only sampled from a confirmed original
// DATA publication followed by a genuine authenticated logical ACK.
func TestRC5BulkACKAgeRequiresConfirmedOriginal(t *testing.T) {
	now := time.Now()
	makeACK := func(accepted, repair bool) *deliveryController {
		d := newDeliveryController()
		r := &deliveryRange{
			key: deliveryKey{stream: 17, offset: 0},
			end: 32 << 10,
			admitted: now.Add(-2 * time.Second),
			flightAtAdmission: 32 << 10,
			repaired: repair,
		}
		if accepted {
			r.accepted = now.Add(-650 * time.Millisecond)
		}
		d.ranges[r.key] = r
		d.bulk = 32 << 10
		d.progress(17, 32 << 10, now)
		return &d
	}
	if d := makeACK(false, false); d.lastBulkACKAgeMS != 0 || !d.lastBulkACKAt.IsZero() {
		t.Fatal("unaccepted frame created physical-to-ACK sample")
	}
	if d := makeACK(true, true); d.lastBulkACKAgeMS != 0 || !d.lastBulkACKAt.IsZero() {
		t.Fatal("repaired frame created original sample")
	}
	d := makeACK(true, false)
	if d.lastBulkACKAgeMS < 640 || d.lastBulkACKAgeMS > 660 || d.lastBulkACKAt.IsZero() {
		t.Fatalf("confirmed physical-to-ACK age = %d", d.lastBulkACKAgeMS)
	}
}
