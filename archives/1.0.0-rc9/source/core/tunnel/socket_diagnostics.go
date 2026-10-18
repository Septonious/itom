package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// SocketFailureKind never returns a socket address, provider reply or arbitrary
// error text. gonet errors may not wrap a syscall, so match only known reasons.
func SocketFailureKind(err error) string {
	if err == nil {
		return "OK"
	}
	if errors.Is(err, context.Canceled) {
		return "CANCELED"
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return "TIMEOUT"
	}
	if errors.Is(err, io.EOF) {
		return "EOF"
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return "CLOSED"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "RESET"
	}
	s := strings.ToLower(err.Error())
	for _, known := range []string{"connection reset", "connection was reset", "broken pipe"} {
		if strings.Contains(s, known) {
			return "RESET"
		}
	}
	return "IO_ERROR"
}

// RecordApplicationEvent records a bounded native TCP bridge outcome. The
// logical stream ID correlates it with OPEN/DATA/RESET; no target is recorded.
func (c *Client) RecordApplicationEvent(stream uint64, direction string, n int64, err error) {
	c.RecordApplicationCopyEvent(stream, direction, n, err, "unknown")
}

// stage identifies the actual failing side of the native copy. Error text and
// addresses remain private; a local app reset must not imply an IMAP failure.
func (c *Client) RecordApplicationCopyEvent(stream uint64, direction string, n int64, err error, stage string) {
	if direction != "upload" && direction != "download" && direction != "upload_half_close" && direction != "download_half_close" {
		return
	}
	switch stage {
	case "read", "write", "eof", "half_close", "unknown":
	default:
		stage = "unknown"
	}
	c.mu.Lock()
	tracer := c.tracer
	c.mu.Unlock()
	if tracer != nil {
		details, _ := json.Marshal(map[string]string{"stage": stage})
		tracer.emit(traceEvent{Event: "application_copy_end", Side: "client", Stream: stream, Name: direction, Offset: uint64(max(0, n)), Code: SocketFailureKind(err), Details: details})
	}
}
