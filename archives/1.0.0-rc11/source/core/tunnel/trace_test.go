package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"itom.local/core/mailbox"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTraceCompactPacketMetadataAndBoundedRotation(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTrace(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Set the limit on a manually constructed synchronous writer to test rotation
	// without racing the live worker's immutable configuration.
	tr.Close()
	f, err := os.OpenFile(filepath.Join(dir, "rotate"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	r := &traceLog{path: f.Name(), file: f, buffer: tr.buffer, limit: 2048}
	r.buffer.Reset(f)
	for i := 0; i < 40; i++ {
		r.write(traceEvent{Event: "packet", Packet: bytes.Repeat([]byte{42}, 512)})
	}
	r.buffer.Flush()
	r.file.Close()
	for _, name := range []string{"rotate", "rotate.1", "rotate.2"} {
		st, e := os.Stat(filepath.Join(dir, name))
		if e != nil || st.Size() > 2048 {
			t.Fatal(st, e)
		}
	}
	if r.writeErrors.Load() != 0 {
		t.Fatal("rotation write errors")
	}
	tr, err = newTrace(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &diagnostic{tracer: tr, account: "a01", side: "client"}
	packet := append([]byte("ITM2"), bytes.Repeat([]byte{17}, 80)...)
	raw, _ := mailbox.Encode(packet)
	d.packet("packet_tx_attempt", 7, mailbox.Message{MIME: raw})
	private, _ := mailbox.Encode([]byte("private-unrelated-mail"))
	d.packet("packet_rx", 8, mailbox.Message{MIME: private})
	clear(packet)
	tr.Close()
	data, err := os.ReadFile(tr.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private-unrelated-mail")) {
		t.Fatal("unrelated body leaked")
	}
	found := false
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var e traceEvent
		if json.Unmarshal(line, &e) != nil {
			t.Fatal("invalid json")
		}
		if e.Operation == 7 {
			found = true
			if len(e.Packet) != 0 {
				t.Fatal("compact trace unexpectedly persisted encrypted packet bytes")
			}
			if e.Digest == "" || e.Bytes != 84 {
				t.Fatalf("compact packet metadata missing: %+v", e)
			}
		}
	}
	if !found || tr.status().WriteErrors != 0 {
		t.Fatal("packet missing", tr.status())
	}
}
func TestTraceOverflowAndConcurrentClose(t *testing.T) {
	tr, e := newTrace(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	tr.emit(traceEvent{Packet: make([]byte, traceQueueBytes+1)})
	if tr.status().Dropped != 1 {
		t.Fatal("oversized trace was not accounted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tr.emit(traceEvent{Event: "concurrent"})
			}
		}()
	}
	tr.Close()
	wg.Wait()
	tr.Close()
	if tr.status().QueueBytes != 0 {
		t.Fatal("queue not drained")
	}
}

func TestObservedMailAggregatesFastEmptyReaderPolls(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTrace(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := &diagnostic{tracer: tr, account: "a01", side: "client"}
	base := &memoryMail{rows: map[string]map[uint32][]byte{"recv": {}}, next: map[string]uint32{"recv": 0}}
	observed := &observedMail{Transport: base, d: d}
	cur := mailbox.Cursor{Validity: 1}
	for i := 0; i < 3; i++ {
		rows, next, err := observed.Page(context.Background(), "recv", cur)
		if err != nil || len(rows) != 0 {
			t.Fatalf("empty poll %d rows=%d err=%v", i, len(rows), err)
		}
		cur = next
	}
	s := d.snapshot()
	if s.FetchPolls != 3 || s.EmptyFetchPolls != 3 || s.FetchMessages != 0 || s.Operations != 3 {
		t.Fatalf("unexpected aggregate counters: %+v", s)
	}
	tr.Close()
	data, err := os.ReadFile(tr.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"name":"fetch_page"`)) {
		t.Fatal("fast empty fetch_page poll was written to trace")
	}
}
