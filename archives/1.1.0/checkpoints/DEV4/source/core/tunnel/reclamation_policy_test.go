package tunnel

import "testing"

func TestRetainedCadenceMissingAuthorityKeepsOriginalTiming(t *testing.T) {
	for _, l := range []*lane{{}, {side: true}} {
		if l.remoteCleanupPeriod() != runtimeRemoteCleanupInterval {
			t.Fatal("unknown capacity accelerated cleanup")
		}
	}
}
