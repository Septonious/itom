package tunnel

import "testing"

func fieldLogLoadState(t *testing.T, f *productionSupervisorFixture, phase string) {
	t.Helper()
	f.client.mu.Lock()
	client := make([]*diagnostic, 0, len(f.client.diagnostics))
	for _, d := range f.client.diagnostics {
		client = append(client, d)
	}
	f.client.mu.Unlock()
	f.mu.Lock()
	server := make([]*diagnostic, 0, len(f.serverLanes))
	for _, l := range f.serverLanes {
		server = append(server, l.diag)
	}
	f.mu.Unlock()
	for side, lines := range map[string][]*diagnostic{"client": client, "server": server} {
		var s LineDiagnostic
		for _, d := range lines {
			if d == nil {
				continue
			}
			x := d.snapshot()
			t.Logf("field_line_diag phase=%s side=%s account=%s ready=%s held_bytes=%d remote_gc=%d pending=%d handoffs=%d governor=%+v", phase, side, x.Account, x.Stage, x.DataBytes, x.RemoteGC, x.PendingDescriptors, x.ReaderHandoffs, x.LatencyGovernor)
			s.ReaderHandoffs += x.ReaderHandoffs
			s.DataAppendCount += x.DataAppendCount
			s.DataAppendBytes += x.DataAppendBytes
			s.ControlAppendCount += x.ControlAppendCount
			s.ControlAppendBytes += x.ControlAppendBytes
			s.DataPayloadBytes += x.DataPayloadBytes
			s.DataBatchRecords += x.DataBatchRecords
			s.DataBatchCount += x.DataBatchCount
			s.FetchPolls += x.FetchPolls
			s.EmptyFetchPolls += x.EmptyFetchPolls
			s.MaxPumpQueueWaitMS = max(s.MaxPumpQueueWaitMS, x.MaxPumpQueueWaitMS)
			s.MaxStripeWaitMS = max(s.MaxStripeWaitMS, x.MaxStripeWaitMS)
			s.MaxLocalGrantWaitMS = max(s.MaxLocalGrantWaitMS, x.MaxLocalGrantWaitMS)
			s.MaxRemoteGrantWaitMS = max(s.MaxRemoteGrantWaitMS, x.MaxRemoteGrantWaitMS)
		}
		t.Logf("field_load_diag phase=%s side=%s reader_handoffs=%d data_appends=%d data_mime_bytes=%d payload_bytes=%d batches=%d records=%d control_appends=%d control_bytes=%d fetches=%d empty_fetches=%d max_pump_wait_ms=%d max_stripe_wait_ms=%d max_local_grant_wait_ms=%d max_remote_grant_wait_ms=%d", phase, side, s.ReaderHandoffs, s.DataAppendCount, s.DataAppendBytes, s.DataPayloadBytes, s.DataBatchCount, s.DataBatchRecords, s.ControlAppendCount, s.ControlAppendBytes, s.FetchPolls, s.EmptyFetchPolls, s.MaxPumpQueueWaitMS, s.MaxStripeWaitMS, s.MaxLocalGrantWaitMS, s.MaxRemoteGrantWaitMS)
	}
}
