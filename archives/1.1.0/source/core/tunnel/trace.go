package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	mailstream "itom.local/mailstream"
)

type traceWriter struct {
	mu       sync.Mutex
	path     string
	disabled bool
}

func newTrace(dir string) (*traceWriter, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("MAILSTREAM_DIAGNOSTICS")
	}
	return &traceWriter{path: diagnosticPath(dir)}, nil
}
func (t *traceWriter) record(value any) error {
	if t.disabled {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > 128<<10 {
		return errors.New("MAILSTREAM_DIAGNOSTIC_LIMIT")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if info, e := os.Stat(t.path); e == nil && info.Size()+int64(len(raw)+1) > 2<<20 {
		_ = os.Remove(t.path + ".2")
		_ = os.Rename(t.path+".1", t.path+".2")
		if err = os.Rename(t.path, t.path+".1"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(t.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	return errors.Join(err, f.Close())
}
func failureCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "CANCELED"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "TIMEOUT"
	case errors.Is(err, mailstream.ErrAuth):
		return "AUTHENTICATION"
	case errors.Is(err, mailstream.ErrCapacity):
		return "CAPACITY"
	case errors.Is(err, mailstream.ErrBusy):
		return "SERVER_BUSY"
	case errors.Is(err, mailstream.ErrStorage):
		return "STORAGE_IO"
	case errors.Is(err, mailstream.ErrProtocol):
		return "PROTOCOL"
	case errors.Is(err, mailstream.ErrClosed):
		return "CARRIER_CLOSED"
	default:
		return "UNAVAILABLE"
	}
}
func (t *traceWriter) event(kind string) {
	_ = t.record(map[string]any{"Event": kind, "Revision": TransportRevision, "SampledAtMS": time.Now().UnixMilli()})
}
