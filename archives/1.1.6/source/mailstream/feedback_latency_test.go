package mailstream

import (
	"testing"
	"time"
)

func TestRC116FeedbackCoalescingFavorsLoadedRTTWithoutRemovingBatchLimit(t *testing.T) {
	link := &IMAPLink{}
	if d := link.feedbackCoalesceDelay(); d != 2*time.Millisecond {
		t.Fatalf("idle feedback delay = %v", d)
	}
	link.lastBulk.Store(time.Now().UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 12*time.Millisecond {
		t.Fatalf("loaded feedback delay = %v", d)
	}
	link.lastBulk.Store(time.Now().Add(-time.Second).UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 2*time.Millisecond {
		t.Fatalf("stale bulk feedback delay = %v", d)
	}
}
