package tunnel

import (
	"context"
	"time"

	"itom.local/core/transport"
)

func (c *Client) Probe() ProbeSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.probe
}

// Sampling is independent of publication/discovery locks and performs no
// provider operation. The writer retains the original rotation and size bounds.
func (c *Client) traceRuntime(ctx context.Context, trace *serverTrace) {
	defer c.workers.Done()
	record := func() {
		var evidence struct {
			Event, Revision string
			SampledAtMS     int64
			Runtime         transport.RuntimeSnapshot
			Probe           ProbeSnapshot
		}
		evidence.Event = "client_runtime"
		evidence.Revision = TransportRevision
		evidence.SampledAtMS = time.Now().UnixMilli()
		evidence.Runtime = c.endpoint.Service.Runtime.Snapshot()
		evidence.Probe = c.Probe()
		_ = trace.record(evidence)
	}
	record()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	defer record()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.endpoint.Done():
			return
		case <-ticker.C:
			record()
		}
	}
}
