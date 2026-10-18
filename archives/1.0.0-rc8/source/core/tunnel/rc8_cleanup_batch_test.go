package tunnel

import (
	"testing"

	"itom.local/core/mailbox"
)

func TestRC8CleanupDiscoveryKeepsByteUIDAndIncarnationBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes uint64
		want  int
	}{
		{"normal-data", 496416, 8},
		{"unknown-size", 0, 2},
		{"small-control", 1024, 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hints []mailbox.AppendHint
			for i := uint32(1); i <= 40; i++ {
				h := mailbox.AppendHint{Validity: 7, UID: i, MIMEBytes: tc.bytes}
				hints = append(hints, h, h) // two connections can retain the same hint
				h.Validity = 8
				hints = append(hints, h)
			}
			validity, ids := cleanupCandidateUIDs(hints)
			if validity != 7 || len(ids) != tc.want {
				t.Fatal("wrong bounded discovery", validity, ids)
			}
			var bytes uint64
			for i, uid := range ids {
				if uid != uint32(i+1) {
					t.Fatal("duplicate or foreign UID", ids)
				}
				size := tc.bytes
				if size == 0 {
					size = mailbox.MaxMIME
				}
				bytes += size
			}
			if bytes > cleanupCandidateBytes || len(ids) > runtimeCleanupAttempts || cleanupCandidateBytes > runtimeCleanupCacheBytes {
				t.Fatal("discovery exceeded existing safety bounds", bytes, len(ids))
			}
		})
	}
	if validity, ids := cleanupCandidateUIDs(nil); validity != 0 || len(ids) != 0 {
		t.Fatal("empty discovery invented a locator")
	}
}
