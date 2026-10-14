package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestApplicationClosureTraceKeepsStreamAndRedactsSocketText(t *testing.T) {
	tracer, err := newTrace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{tracer: tracer}
	client.RecordApplicationEvent(155, "download", 12345, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET})
	client.RecordApplicationCopyEvent(157, "upload", 80, errors.New("secret-password destination-private provider-reply"), "secret-password")
	client.RecordApplicationEvent(159, "untrusted-operation secret-password", 0, nil)
	tracer.Close()
	raw, err := os.ReadFile(tracer.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret-password")) || bytes.Contains(raw, []byte("destination-private")) {
		t.Fatal("socket text leaked")
	}
	found := 0
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var event traceEvent
		if json.Unmarshal(line, &event) != nil {
			t.Fatal("invalid trace")
		}
		if event.Event != "application_copy_end" {
			continue
		}
		found++
		if event.Stream == 155 && (event.Code != "RESET" || event.Offset != 12345) {
			t.Fatal("lost stream outcome")
		}
		if event.Stream == 157 && event.Code != "IO_ERROR" {
			t.Fatal("unbounded error classification")
		}
		if event.Stream == 157 && string(event.Details) != `{"stage":"unknown"}` {
			t.Fatal("unbounded native copy stage", string(event.Details))
		}
	}
	if found != 2 {
		t.Fatal("invalid event accepted or valid outcome missing", found)
	}
}
