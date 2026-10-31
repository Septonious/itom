package transport

import "testing"

// This is parser allocation/CPU evidence ONLY, not RTT or tunnel bandwidth.
// Repeated inventory of a known UID still parses and bounds each actual reply.
func BenchmarkRepeatedKnownUIDMetadata(b *testing.B) {
	op := &operation{metadata: true, first: 1, last: 1, uids: map[uint32]bool{1: true}}
	e := &Executor{fetch: op, limits: DefaultIMAPLimits()}
	b.ReportAllocs()
	for b.Loop() {
		op.bytes = 0
		ok, err := e.readMetadata("* 1 FETCH (UID 1 RFC822.SIZE 1234)")
		if err != nil || !ok {
			b.Fatal(ok, err)
		}
	}
}
