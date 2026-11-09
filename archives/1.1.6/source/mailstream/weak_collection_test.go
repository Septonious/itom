package mailstream

import (
	"testing"
	"time"
)

func TestPoorNetworkCollectionRetryDeadlineIsBounded(t *testing.T) {
	if got:=collectionAttemptTimeout(false);got!=8*time.Second {
		t.Fatalf("normal collector deadline changed: %v",got)
	}
	if got:=collectionAttemptTimeout(true);got!=18*time.Second {
		t.Fatalf("degraded collector has no retry budget: %v",got)
	}
	if collectionRetryMessages!=32 || collectionPageBytes>4<<20 {
		t.Fatal("degraded collection must retain bounded page limits")
	}
}
