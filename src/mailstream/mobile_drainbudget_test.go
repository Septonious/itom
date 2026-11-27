package mailstream

import "testing"

func TestMobileUploadPendingByteLimit(t *testing.T) {
	for _, flight := range []int{2 * maxWire, 512 << 10, 8 << 20, maxPendingBytes} {
		if got := pendingByteLimit(flight, true); got != min(maxPendingBytes, flight+maxWire) {
			t.Fatal(flight, got)
		}
		if got := pendingByteLimit(flight, false); got != min(maxPendingBytes, 2*flight) {
			t.Fatal(flight, got)
		}
	}
}
