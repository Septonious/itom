package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"itom.local/core/journal"
	"itom.local/core/transport"
)

// One shared writer per service, including startup before any carrier exists.
// It never receives provider response strings, credentials or traffic bodies.
type serverTrace struct {
	mu      sync.Mutex
	root    string
	enabled bool
}

func (t *serverTrace) record(value any) error {
	if t == nil || !t.enabled {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > 256<<10 {
		raw, _ = json.Marshal(map[string]any{"Event": "snapshot_oversize", "Revision": TransportRevision, "Bytes": len(raw), "SampledAtMS": time.Now().UnixMilli()})
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err = os.MkdirAll(t.root, 0700); err != nil {
		return err
	}
	path := filepath.Join(t.root, "transport-trace.jsonl")
	if info, e := os.Stat(path); e == nil && info.Size()+int64(len(raw)+1) > 2<<20 {
		_ = os.Remove(path + ".2")
		if e = os.Rename(path+".1", path+".2"); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = os.Rename(path, path+".1"); e != nil {
			return e
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}
func (t *serverTrace) snapshot(user string, raw []byte) error {
	if t == nil || !t.enabled {
		return nil
	}
	return writePrivateSnapshot(filepath.Join(t.root, "v2-"+user+".json"), raw)
}

var errProviderUIDPlus = errors.New("V2_PROVIDER_UIDPLUS_REQUIRED")
var errProviderKeywords = errors.New("V2_PROVIDER_PERMANENT_KEYWORDS_REQUIRED")

func startupFailureCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT"
	case errors.Is(err, journal.ErrConfig):
		return "JOURNAL_CONFIG_MISMATCH"
	case errors.Is(err, errProviderUIDPlus):
		return "UIDPLUS_UNSUPPORTED"
	case errors.Is(err, errProviderKeywords):
		return "PERMANENT_KEYWORDS_UNSUPPORTED"
	case errors.Is(err, transport.ErrRefused):
		return "IMAP_TAGGED_REFUSAL"
	case errors.Is(err, transport.ErrProtocol):
		return "PROTOCOL_OR_RETAINED_BODY"
	case errors.Is(err, transport.ErrCapacity):
		return "CAPACITY_OR_INVENTORY_BOUND"
	default:
		if stage := transport.IMAPStartupStage(err); stage != "" {
			return "IMAP_" + stage + "_FAILED"
		}
		return "LOCAL_OR_SESSION_FAILURE"
	}
}

type selectionEvent struct {
	Account, Stage, Role, Failure string
	UIDPlus, Keywords             bool
}
