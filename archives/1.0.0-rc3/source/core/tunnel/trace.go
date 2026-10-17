package tunnel

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

const traceFileBytes = 16 << 20
const traceQueueBytes = 4 << 20

// Full encrypted transport packets, never credentials or plaintext TCP payload.
// []byte is base64 in JSON. Disk I/O and base64 encoding run only in the worker.
type traceEvent struct {
	Run        string          `json:"run"`
	Seq        uint64          `json:"seq"`
	Time       string          `json:"time"`
	Event      string          `json:"event"`
	Account    string          `json:"account,omitempty"`
	User       string          `json:"user,omitempty"`
	Side       string          `json:"side,omitempty"`
	Session    string          `json:"session,omitempty"`
	Operation  uint64          `json:"operation,omitempty"`
	Name       string          `json:"name,omitempty"`
	Code       string          `json:"code,omitempty"`
	DurationUS int64           `json:"duration_us,omitempty"`
	Stream     uint64          `json:"stream,omitempty"`
	Logical    uint64          `json:"logical,omitempty"`
	Offset     uint64          `json:"offset,omitempty"`
	Counter    *uint64         `json:"counter,omitempty"`
	Bytes      int             `json:"bytes,omitempty"`
	UID        uint32          `json:"uid,omitempty"`
	Validity   uint32          `json:"uidvalidity,omitempty"`
	Digest     string          `json:"sha256,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	Packet     []byte          `json:"encrypted_packet_base64,omitempty"`
}
type traceStatus struct {
	Enabled                       bool
	Written, Dropped, WriteErrors uint64
	QueueBytes                    int64
	MaxFileBytes                  int
	Files                         int
}
type traceLog struct {
	mu                                             sync.Mutex
	closed                                         bool
	queue                                          chan traceEvent
	done                                           chan struct{}
	runID                                          string
	path                                           string
	size                                           int64
	file                                           *os.File
	buffer                                         *bufio.Writer
	limit                                          int64
	seq, operations, written, dropped, writeErrors atomic.Uint64
	queued                                         atomic.Int64
}

func newTrace(dir string) (*traceLog, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "transport-trace.jsonl")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return nil, e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	t := &traceLog{runID: fmt.Sprintf("%d", time.Now().UnixNano()), path: path, file: f, buffer: bufio.NewWriterSize(f, 64<<10), size: st.Size(), limit: traceFileBytes, queue: make(chan traceEvent, 512), done: make(chan struct{})}
	go t.run()
	t.emit(traceEvent{Event: "trace_start", Name: TransportRevision, Details: json.RawMessage(`{"schema":2,"packet_encoding":"sha256+metadata","compact":true,"plaintext_payload":false,"files":3,"max_file_bytes":16777216}`)})
	return t, nil
}
func (t *traceLog) emit(e traceEvent) {
	if t == nil {
		return
	}
	n := int64(len(e.Packet) + len(e.Details) + 512)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if n > traceQueueBytes || t.queued.Load()+n > traceQueueBytes {
		t.dropped.Add(1)
		return
	}
	e.Run = t.runID
	e.Seq = t.seq.Add(1)
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	e.Packet = bytes.Clone(e.Packet)
	e.Details = bytes.Clone(e.Details)
	t.queued.Add(n)
	select {
	case t.queue <- e:
	default:
		t.queued.Add(-n)
		t.dropped.Add(1)
	}
}
func (t *traceLog) write(e traceEvent) {
	raw, err := json.Marshal(e)
	if err == nil {
		raw = append(raw, '\n')
		if t.size+int64(len(raw)) > t.limit {
			err = t.rotate()
		}
		if err == nil {
			_, err = t.buffer.Write(raw)
			t.size += int64(len(raw))
		}
	}
	if err != nil {
		t.writeErrors.Add(1)
	} else {
		t.written.Add(1)
	}
}
func (t *traceLog) rotate() error {
	if e := t.buffer.Flush(); e != nil {
		return e
	}
	if e := t.file.Close(); e != nil {
		return e
	}
	// Rename is confined to three dedicated files; no journal/config paths.
	_ = os.Remove(t.path + ".2")
	if e := os.Rename(t.path+".1", t.path+".2"); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e := os.Rename(t.path, t.path+".1"); e != nil {
		return e
	}
	f, e := os.OpenFile(t.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	t.file = f
	t.buffer.Reset(f)
	t.size = 0
	return nil
}
func (t *traceLog) run() {
	defer close(t.done)
	defer func() { _ = t.file.Close() }()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var reported uint64
	for {
		select {
		case e, ok := <-t.queue:
			if !ok {
				details, _ := json.Marshal(t.status())
				t.write(traceEvent{Run: t.runID, Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "trace_stop", Details: details})
				if err := t.buffer.Flush(); err != nil {
					t.writeErrors.Add(1)
				}
				return
			}
			t.write(e)
			t.queued.Add(-int64(len(e.Packet) + len(e.Details) + 512))
		case <-tick.C:
			if n := t.dropped.Load(); n != reported {
				details, _ := json.Marshal(map[string]uint64{"dropped_total": n})
				t.write(traceEvent{Run: t.runID, Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "trace_drop", Details: details})
				reported = n
			}
			if e := t.buffer.Flush(); e != nil {
				t.writeErrors.Add(1)
			}
		}
	}
}
func (t *traceLog) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if !t.closed {
		t.closed = true
		close(t.queue)
	}
	t.mu.Unlock()
	<-t.done
}
func (t *traceLog) status() traceStatus {
	if t == nil {
		return traceStatus{}
	}
	return traceStatus{true, t.written.Load(), t.dropped.Load(), t.writeErrors.Load(), t.queued.Load(), traceFileBytes, 3}
}
func (d *diagnostic) trace(e traceEvent) {
	if d == nil || d.tracer == nil {
		return
	}
	e.Account = d.account
	e.User = d.user
	e.Side = d.side
	d.tracer.emit(e)
}
func (d *diagnostic) packet(event string, operation uint64, m mailbox.Message) {
	if d == nil || d.tracer == nil {
		return
	}
	body, err := mailbox.Decode(m.MIME)
	if err != nil {
		d.trace(traceEvent{Event: "packet_rejected", Operation: operation, Code: "MIME_FORMAT", Bytes: len(m.MIME), UID: m.UID, Validity: m.Validity})
		return
	}
	recognized := bytes.HasPrefix(body, []byte(transportHelloMagic)) || bytes.HasPrefix(body, []byte(transportProbeMagic)) || bytes.HasPrefix(body, []byte(transportChallengeMagic)) || bytes.HasPrefix(body, []byte(wire.DataBatchMagic)) || bytes.HasPrefix(body, []byte(wire.DataEnvelopeMagic)) || bytes.HasPrefix(body, []byte("ITB1")) || bytes.HasPrefix(body, []byte("ITC1")) || bytes.HasPrefix(body, []byte("ITQ1")) || bytes.HasPrefix(body, []byte("ITM2"))
	hash := sha256.Sum256(body)
	e := traceEvent{Event: event, Operation: operation, Bytes: len(body), UID: m.UID, Validity: m.Validity, Digest: hex.EncodeToString(hash[:])}
	if len(body) >= wire.HeaderSize && bytes.HasPrefix(body, []byte("ITM2")) {
		if h, err := wire.ParseHeader(body[:wire.HeaderSize]); err == nil {
			e.Counter = &h.Counter
			e.Session = fmt.Sprintf("%x", h.Session)
		}
	}
	// Gate16 proved that embedding every encrypted MIME body in JSON could rotate
	// ~48 MiB of trace in only a few seconds during real traffic. The SHA-256,
	// UID/UIDVALIDITY, session/counter and frame-level events are sufficient to
	// correlate transport behavior without base64-copying every DATA packet. Keep
	// unrelated mail explicitly marked, but never persist its body either.
	if !recognized {
		e.Code = "UNRECOGNIZED_BODY"
	}
	d.trace(e)
}

func compactFrameTrace(event string, f wire.Frame, counter *uint64) bool {
	return compactFrameTraceSize(event, f, counter, len(f.Payload))
}

func compactFrameTraceSize(event string, f wire.Frame, counter *uint64, size int) bool {
	// DATA is already represented by packet metadata and data_batch diagnostics.
	// Sample it deterministically to retain stream/counter breadcrumbs without
	// turning diagnostics into a second high-bandwidth workload.
	if f.Type == wire.Data {
		// FrameInfo deliberately strips payload. Zero length is unknown size,
		// not proof of an interactive packet; keep its bulk sampling policy.
		if size > 0 && size <= datapath.InteractiveDataThreshold {
			return true
		}
		return counter != nil && *counter%32 == 0 || counter == nil && f.Offset%(1<<20) == 0
	}
	if f.Type == wire.Status && f.Offset == 0 && len(f.Payload) == 33 && f.Payload[32] == 0 {
		return event != "frame_queued" && counter != nil && *counter%16 == 0
	}
	// WINDOW/RECEIPT/MAIL_RECEIPT/GC_STATUS dominate steady-state frame chatter.
	// Health snapshots retain their aggregate state; keep only periodic physical
	// samples. Logical queue events for these routine frames add little value.
	switch f.Type {
	case wire.Window, wire.Receipt, wire.MailReceipt, wire.GCStatus:
		if event == "frame_queued" || counter == nil {
			return false
		}
		return *counter%16 == 0
	default:
		return true
	}
}

func (l *lane) traceFrame(event string, f wire.Frame, counter *uint64) {
	l.traceFrameSize(event, f, counter, len(f.Payload))
}

func (l *lane) traceFrameSize(event string, f wire.Frame, counter *uint64, size int) {
	if l.diag == nil || l.diag.tracer == nil || !compactFrameTraceSize(event, f, counter, size) {
		return
	}
	var details any
	if f.Type != wire.Data {
		ctrl, err := wire.DecodeControl(f)
		if err == nil {
			switch m := ctrl.(type) {
			case wire.Connect:
				details = map[string]any{"host_redacted": true, "port": m.Port}
			case wire.UsageReport:
				// Folder names can include user-supplied private labels.
				details = map[string]any{"key": m.Key, "total": m.Total}
				if m.Attempt != nil {
					details = map[string]any{"key": m.Key, "total": m.Total, "batch": m.Attempt.Batch, "encoded_bytes": m.Attempt.EncodedBytes, "decoded_bytes": m.Attempt.DecodedBytes, "digest": m.Attempt.Digest}
				}
			default:
				details = ctrl
			}
		}
	}
	raw, _ := json.Marshal(details)
	session := l.cipher.Context().Session
	l.diag.trace(traceEvent{Event: event, Name: f.Type.String(), Session: fmt.Sprintf("%x", session), Stream: f.Stream, Logical: f.Logical, Offset: f.Offset, Counter: counter, Bytes: size, Details: raw})
}

// Socket failures used to be reduced to ReasonClosed on the wire. Preserve a
// safe operation/reason breadcrumb without logging the target or error text.
func (l *lane) traceSocketFailure(stream uint64, operation string, err error) {
	if l.diag != nil {
		l.diag.trace(traceEvent{Event: "socket_failure", Stream: stream, Name: operation, Code: SocketFailureKind(err)})
	}
}
