package tunnel

import (
	"testing"
	"time"
)

// Exercise the actual supervisors with tracing disabled: UI metrics must not
// depend on trace files, and each logical TCP stream belongs to one account.
func TestAccountMetricsWithoutTracing(t *testing.T) {
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "0")
	f := newProductionSupervisorFixture(t)
	p := f.openOn(t, "a01")
	if err := supervisorEcho(p, 1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := f.client.Snapshot()
		var up, down uint64
		var streams int
		for _, line := range s.Lines {
			if line.Extended {
				t.Fatal("metrics enabled full tracing")
			}
			up += line.UploadBytes
			down += line.DownloadBytes
			streams += line.TCPStreams
			if line.Account == "a02" && line.TCPStreams != 0 {
				t.Fatal("sibling account counted the same logical TCP stream")
			}
		}
		if streams == 1 && s.TCP == 1 && up > 0 && down > 0 {
			if f.client.tracer != nil || len(s.Events) != 0 {
				t.Fatal("metrics created packet tracing or event logs")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("current account metrics not populated", f.client.Snapshot())
}
