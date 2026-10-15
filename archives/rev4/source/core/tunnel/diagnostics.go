package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
)

// No hostnames, target domains, mailbox names, keys or credentials are logged.
type LineDiagnostic struct {
	Scheduler                                              datapath.SchedulerSnapshot
	RTTSampleAgeMS, RTTSmoothedMillis, RTTIdleMillis       int64
	RTTFresh                                               bool
	RTTSampleKind                                          string
	UploadBytes, DownloadBytes                             uint64
	TCPStreams                                             int
	IMAPLatencyMillis                                      int64
	ControlSuppressed                                      uint64
	DataCommits, ControlCommits                            journal.CommitSnapshot
	ReaderRepairs                                          uint64
	DataFetchPolls, DataEmptyFetchPolls, DataFetchMessages uint64
	DataIdleWaits, DataIdleNotifies                        uint64
	DataReceiveLatencyMillis                               int64
	DeferredAdmissions                                     uint64
	Ready                                                  bool
	InitialReadyMS                                         int64 // Latest connection attempt: start through authenticated application readiness.
	Extended                                               bool
	ActiveLines, TotalLines                                int
	ReadinessReason                                        string

	ReaderHandoffs                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           int64
	LatencyGovernor                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          latencyGovernorSnapshot
	PendingRetirements, AvailableGrants, IssuedGrants, PendingDescriptors, RemoteGC, Obsolete, Known                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         int
	IOConnections                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            int
	ReaderIdle                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               bool
	LastOrigin                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               string
	ControlWaitMS, MaxControlWaitMS, AppendCount, AppendBytes, ControlAppendCount, ControlAppendBytes, DataAppendCount, DataAppendBytes, FetchPolls, EmptyFetchPolls, FetchMessages, IdleWaits, IdleNotifies, IdleAudits, IdleKicks                                                                                                                                                                                                                                                                                                                                                                          int64
	WindowSent, WindowReceived, ReceiptSent, ReceiptReceived                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 int64
	MaxWindowSent, MaxWindowReceived                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         uint64
	DataWaitMS, MaxDataWaitMS, FastDataWaitMS, MaxFastDataWaitMS, LocalGrantWaitMS, MaxLocalGrantWaitMS, RemoteGrantWaitMS, MaxRemoteGrantWaitMS, LocalGrantHeadroomWaits, LocalGrantLimitWaits, PumpQueueWaitMS, MaxPumpQueueWaitMS, FastPumpQueueWaitMS, MaxFastPumpQueueWaitMS, StripeWaitMS, MaxStripeWaitMS, FastStripeWaitMS, MaxFastStripeWaitMS, GrantSelectWaitMS, MaxGrantSelectWaitMS, FastGrantSelectWaitMS, MaxFastGrantSelectWaitMS, BulkReplacementGrants, DataAttempts, DataRecords, DataPayloadBytes, DataBatchCount, DataBatchRecords, DataBatchBodyBytes, ControlRecords, ReceivedRecords int64
	CleanupMS, CleanupMessages, CleanupRemoteRuns, CleanupRemoteMessages, CleanupLocalDataRuns, CleanupLocalDataMessages, CleanupLocalControlRuns, CleanupLocalControlMessages, CleanupPresenceRuns                                                                                                                                                                                                                                                                                                                                                                                                          int64
	Account, Stage, LastOperation, LastError, LastConnectStage, LastConnectReason                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            string
	Reconnects, Queue, Outbox                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                int
	ControlMessages, DataMessages, DataBytes, PeakDataHeldBytes, PeakDataHeldMessages                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        uint64
	LastOperationMS, MaxOperationMS, Operations, RXAgeMS, RTTMillis                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          int64
}
type diagnostic struct {
	metricsOnly         bool
	tracer              *traceLog
	account, user, side string
	liveFolder          string
	mu                  sync.Mutex
	line                LineDiagnostic
	log                 func(string)
	lastHealth          time.Time
	connectingAt        time.Time
}

func errorCode(err error) string {
	if err == nil {
		return "OK"
	}
	for _, candidate := range []struct {
		err  error
		code string
	}{
		{errDeliveryDeferred, "DELIVERY_MEMBER_DEFERRED"},
		{errDeliveryStopped, "DELIVERY_STREAM_STOPPED"},
		{errDeliveryLimit, "DELIVERY_FLIGHT_LIMIT"},
	} {
		if errors.Is(err, candidate.err) {
			return candidate.code
		}
	}
	for _, x := range []struct {
		err  error
		code string
	}{{secure.ErrReplay, "REPLAY"}, {secure.ErrAuth, "AUTHENTICATION"}, {secure.ErrContext, "CRYPTO_CONTEXT"}, {secure.ErrKeyLimit, "KEY_LIMIT"}} {
		if errors.Is(err, x.err) {
			return x.code
		}
	}
	if errors.Is(err, datapath.ErrState) {
		return "DATA_STATE"
	}
	if errors.Is(err, context.Canceled) {
		return "CANCELED"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "TIMEOUT"
	}
	var n net.Error
	if errors.As(err, &n) && n.Timeout() {
		return "TIMEOUT"
	}
	// Only codes defined by this application, never an arbitrary provider reply.
	for _, code := range []string{"JOURNAL_REMOTE_COUNTER", "JOURNAL_RETIRED_OR_UNKNOWN", "JOURNAL_GENERATION", "JOURNAL_INVENTORY_REQUIRED", "JOURNAL_STATE", "JOURNAL_IO_UNCERTAIN", "COORDINATOR_NOT_READY", "REMOTE_DESCRIPTOR_MISSING", "DESCRIPTOR_LIMIT", "GRANT_SCOPE", "GRANT_DIRECTION"} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	for _, code := range []string{"WALLET_EXHAUSTED", "JOURNAL_LIMIT", "JOURNAL_RECOVERY_REQUIRED", "JOURNAL_CORRUPT", "IMAP_APPEND_OUTCOME_UNKNOWN", "IMAP_CONNECT_FAILED", "IMAP_TLS_FAILED", "IMAP_AUTH_OR_GREETING_FAILED", "IMAP_UIDVALIDITY_CHANGED", "IMAP_LIMIT", "IMAP_SCOPE", "SESSION_SUPERSEDED", "SESSION_NOT_ACTIVATED", "COORDINATOR_NOT_READY", "CARRIER_REATTACH", "CARRIER_REKEY", "SESSION_DEADLINE", "SESSION_STREAM_LIMIT", "TCP_OPEN_TIMEOUT", "TRANSPORT_TIMEOUT", "CONTROL_QUEUE_LIMIT", "RECEIPT_WINDOW_LIMIT", "LANE_ROTATION_REQUIRED", "WIRE_FORMAT", "REMOTE_GC_INCOMPLETE", "WALLET_STATE", "WALLET_MISSING", "VPN_PROTECT_FAILED", "UNDERLYING_DNS_MISSING", "IMAP_CREATE_FAILED", "JOURNAL_SCOPE", "JOURNAL_MISSING"} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return "IO_OR_PROTOCOL_ERROR"
}
func (d *diagnostic) snapshot() LineDiagnostic { d.mu.Lock(); defer d.mu.Unlock(); return d.line }
func (d *diagnostic) event(text string) {
	d.trace(traceEvent{Event: "lifecycle", Name: text})
	if d != nil && d.log != nil {
		d.log(text)
	}
}
func (l *lane) stage(stage string) {
	if l.diag == nil {
		return
	}
	d := l.diag
	d.mu.Lock()
	old := d.line.Stage
	d.line.Stage = stage
	if stage == "ready" {
		d.line.LastError = ""
		if old != "ready" && !d.connectingAt.IsZero() {
			d.line.InitialReadyMS = time.Since(d.connectingAt).Milliseconds()
		}
	}
	d.mu.Unlock()
	if old != stage {
		d.event("event=phase stage=" + stage)
	}
}
func (l *lane) health() {
	if l.diag == nil || l.diag.metricsOnly {
		return
	}
	d := l.diag
	l.mu.Lock()
	out := len(l.out)
	obsolete := len(l.obsolete)
	l.mu.Unlock()
	c, _ := l.control.Held(l.account.ID, l.user.ID)
	b, _ := l.data.Held(l.account.ID, l.user.ID)
	gov := l.stripe.deliverySnapshot()
	d.mu.Lock()
	d.line.LatencyGovernor = gov
	d.line.Scheduler = l.pump.SchedulerSnapshot()
	d.line.ControlSuppressed = l.controlRepeat.suppressed.Load()
	d.line.DeferredAdmissions = l.deferredAdmissionTrace.total.Load()
	d.line.DataCommits, d.line.ControlCommits = l.data.CommitSnapshot(), l.control.CommitSnapshot()
	d.line.Queue = len(l.controlQueue) + len(l.urgentQueue) + len(l.maintenanceQueue)
	d.line.Outbox = out
	d.line.PendingRetirements = len(l.retireSent)
	d.line.AvailableGrants = len(l.grants)
	d.line.IssuedGrants = len(l.issued)
	d.line.PendingDescriptors = len(l.pending)
	d.line.RemoteGC = len(l.remoteGC)
	d.line.Obsolete = obsolete
	d.line.Known = len(l.known)
	d.line.ControlMessages = c.Messages
	d.line.DataMessages = b.Messages
	d.line.DataBytes = b.Bytes
	if !l.lastRX.IsZero() {
		d.line.RXAgeMS = time.Since(l.lastRX).Milliseconds()
	}
	applyRTTDiagnostics(&d.line, l, time.Now())
	emit := time.Since(d.lastHealth) >= 15*time.Second
	if emit {
		d.lastHealth = time.Now()
	}
	s := d.line
	d.mu.Unlock()
	if emit {
		status := traceStatus{}
		if d.tracer != nil {
			status = d.tracer.status()
		}
		details, _ := json.Marshal(struct {
			Line  LineDiagnostic
			Trace traceStatus
		}{s, status})
		d.trace(traceEvent{Event: "health", Details: details})
		d.event(fmt.Sprintf("event=flow_health data_wait_ms=%d max_data_wait_ms=%d fast_data_wait_ms=%d max_fast_data_wait_ms=%d pump_queue_wait_ms=%d max_pump_queue_wait_ms=%d fast_pump_queue_wait_ms=%d max_fast_pump_queue_wait_ms=%d stripe_wait_ms=%d max_stripe_wait_ms=%d fast_stripe_wait_ms=%d max_fast_stripe_wait_ms=%d grant_select_wait_ms=%d max_grant_select_wait_ms=%d fast_grant_select_wait_ms=%d max_fast_grant_select_wait_ms=%d bulk_replacements=%d data_attempts=%d data_records=%d control_records=%d received_records=%d pending_retirements=%d available_grants=%d issued_grants=%d trace_dropped=%d trace_errors=%d", s.DataWaitMS, s.MaxDataWaitMS, s.FastDataWaitMS, s.MaxFastDataWaitMS, s.PumpQueueWaitMS, s.MaxPumpQueueWaitMS, s.FastPumpQueueWaitMS, s.MaxFastPumpQueueWaitMS, s.StripeWaitMS, s.MaxStripeWaitMS, s.FastStripeWaitMS, s.MaxFastStripeWaitMS, s.GrantSelectWaitMS, s.MaxGrantSelectWaitMS, s.FastGrantSelectWaitMS, s.MaxFastGrantSelectWaitMS, s.BulkReplacementGrants, s.DataAttempts, s.DataRecords, s.ControlRecords, s.ReceivedRecords, s.PendingRetirements, s.AvailableGrants, s.IssuedGrants, status.Dropped, status.WriteErrors))
		d.event(fmt.Sprintf("event=health stage=%s rtt_ms=%d rx_age_ms=%d control_queue=%d outbox=%d control_held=%d data_held=%d last_op=%s op_ms=%d max_op_ms=%d control_wait_ms=%d max_control_wait_ms=%d gc_ms=%d gc_messages=%d append_count=%d append_bytes=%d fetch_polls=%d empty_fetch_polls=%d fetch_messages=%d reader_idle=%t idle_waits=%d idle_notifies=%d idle_audits=%d idle_kicks=%d", s.Stage, s.RTTMillis, s.RXAgeMS, s.Queue, s.Outbox, s.ControlMessages, s.DataMessages, s.LastOperation, s.LastOperationMS, s.MaxOperationMS, s.ControlWaitMS, s.MaxControlWaitMS, s.CleanupMS, s.CleanupMessages, s.AppendCount, s.AppendBytes, s.FetchPolls, s.EmptyFetchPolls, s.FetchMessages, s.ReaderIdle, s.IdleWaits, s.IdleNotifies, s.IdleAudits, s.IdleKicks))
	}
}
func (d *diagnostic) reconnect(err error, delay time.Duration) {
	d.mu.Lock()
	stage := d.line.Stage
	op := d.line.LastOperation
	d.line.LastError = errorCode(err)
	d.line.LastOrigin = errorOrigin(err)
	control, data := d.line.ControlMessages, d.line.DataMessages
	d.line.Reconnects++
	d.line.Stage = "backoff"
	d.mu.Unlock()
	d.event(fmt.Sprintf("event=reconnect stage=%s operation=%s origin=%s code=%s control_held=%d data_held=%d retry_ms=%d", stage, op, errorOrigin(err), errorCode(err), control, data, delay.Milliseconds()))
}

type observedMail struct {
	mailbox.Transport
	d *diagnostic
}

func (m *observedMail) op(name string, fn func() error) error {
	return m.operation(name, func(uint64) error { return fn() })
}
func (m *observedMail) finishOperation(name string, start time.Time, e error) error {
	ms := time.Since(start).Milliseconds()
	detail := mailbox.ScopeDetail(e)
	reason := mailbox.FailureReason(e)
	m.d.mu.Lock()
	m.d.line.LastOperation = name
	m.d.line.LastOperationMS = ms
	m.d.line.Operations++
	m.d.line.MaxOperationMS = max(m.d.line.MaxOperationMS, ms)
	if e == nil && (name == "fetch_page" || name == "append_data" || name == "append_control" || name == "append") {
		if m.d.line.IMAPLatencyMillis == 0 {
			m.d.line.IMAPLatencyMillis = ms
		} else {
			m.d.line.IMAPLatencyMillis = (7*m.d.line.IMAPLatencyMillis + ms) / 8
		}
	}
	if e != nil {
		m.d.line.LastError = errorCode(e)
	}
	m.d.mu.Unlock()
	if e != nil {
		message := fmt.Sprintf("event=imap_error operation=%s duration_ms=%d code=%s reason=%s", name, ms, errorCode(e), reason)
		if detail != "" && detail != mailbox.ErrScope.Error() {
			message += " detail=" + detail
		}
		m.d.event(message)
		return fmt.Errorf("%s: %w", name, e)
	}
	return nil
}

func (m *observedMail) operation(name string, fn func(uint64) error) error {
	var id uint64
	if m.d.tracer != nil {
		id = m.d.tracer.operations.Add(1)
	}
	m.d.trace(traceEvent{Event: "imap_begin", Name: name, Operation: id})
	start := time.Now()
	e := fn(id)
	detail := mailbox.ScopeDetail(e)
	var traceDetails json.RawMessage
	if m.d.tracer != nil {
		fields := map[string]any{}
		if detail != "" && detail != mailbox.ErrScope.Error() {
			fields["detail"] = detail
		}
		if sockets, ok := m.Transport.(interface {
			SocketSnapshots() []mailbox.SocketSnapshot
		}); ok {
			fields["sockets"] = sockets.SocketSnapshots()
		}
		traceDetails, _ = json.Marshal(fields)
	}
	m.d.trace(traceEvent{Event: "imap_end", Name: name, Operation: id, Code: errorCode(e), DurationUS: time.Since(start).Microseconds(), Details: traceDetails})
	return m.finishOperation(name, start, e)
}
func (m *observedMail) Ensure(c context.Context, f string) error {
	return m.op("ensure", func() error { return m.Transport.Ensure(c, f) })
}
func (m *observedMail) appendObserved(name string, c context.Context, f string, b []byte, appendFn func(context.Context, string, []byte) error) error {
	err := m.operation(name, func(id uint64) error {
		m.d.packet("packet_tx_attempt", id, mailbox.Message{MIME: b})
		return appendFn(c, f, b)
	})
	if err == nil {
		m.d.mu.Lock()
		m.d.line.AppendCount++
		m.d.line.AppendBytes += int64(len(b))
		switch name {
		case "append_data":
			m.d.line.DataAppendCount++
			m.d.line.DataAppendBytes += int64(len(b))
		case "append_control":
			m.d.line.ControlAppendCount++
			m.d.line.ControlAppendBytes += int64(len(b))
		}
		m.d.mu.Unlock()
	}
	return err
}
func (m *observedMail) Append(c context.Context, f string, b []byte) error {
	return m.appendObserved("append", c, f, b, m.Transport.Append)
}
func (m *observedMail) AppendControl(c context.Context, f string, b []byte) error {
	if split, ok := m.Transport.(interface {
		AppendControl(context.Context, string, []byte) error
	}); ok {
		return m.appendObserved("append_control", c, f, b, split.AppendControl)
	}
	return m.Append(c, f, b)
}
func (m *observedMail) AppendData(c context.Context, f string, b []byte) error {
	if split, ok := m.Transport.(interface {
		AppendData(context.Context, string, []byte) error
	}); ok {
		return m.appendObserved("append_data", c, f, b, split.AppendData)
	}
	return m.Append(c, f, b)
}
func (m *observedMail) AppendDataPriority(c context.Context, f string, b []byte, fast bool) error {
	if split, ok := m.Transport.(interface {
		AppendDataPriority(context.Context, string, []byte, bool) error
	}); ok {
		return m.appendObserved("append_data", c, f, b, func(c context.Context, f string, b []byte) error { return split.AppendDataPriority(c, f, b, fast) })
	}
	return m.AppendData(c, f, b)
}
func (m *observedMail) DataParallelism() int {
	if split, ok := m.Transport.(interface{ DataParallelism() int }); ok {
		return split.DataParallelism()
	}
	return 1
}
func (m *observedMail) DataAvailable() bool {
	if split, ok := m.Transport.(interface{ DataAvailable() bool }); ok {
		return split.DataAvailable()
	}
	return true
}
func (m *observedMail) SetDataWake(wake func()) {
	if split, ok := m.Transport.(interface{ SetDataWake(func()) }); ok {
		capacity := func() int32 {
			if !m.DataAvailable() {
				return 0
			}
			return int32(m.DataParallelism())
		}
		var previous atomic.Int32
		previous.Store(capacity())
		split.SetDataWake(func() {
			n := capacity()
			if old := previous.Swap(n); old != n && m.d != nil {
				if io, ok := m.Transport.(*laneMail); ok {
					connections := io.connectionCount()
					if connections > 0 {
						m.d.mu.Lock()
						m.d.line.IOConnections = connections
						m.d.mu.Unlock()
						m.d.event(fmt.Sprintf("event=data_capacity before=%d current=%d lane_connections=%d", old, n, connections))
					}
				}
			}
			if wake != nil {
				wake()
			}
		})
	}
}
func (m *observedMail) DataMaintenanceIsolated() bool {
	io, ok := m.Transport.(interface{ DataMaintenanceIsolated() bool })
	return ok && io.DataMaintenanceIsolated()
}
func (m *observedMail) SetReaderHot(until time.Time) {
	if hot, ok := m.Transport.(interface{ SetReaderHot(time.Time) }); ok {
		hot.SetReaderHot(until)
	}
}
func (m *observedMail) WaitRead(ctx context.Context, folder string, maxWait time.Duration) (used, notified bool, err error) {
	waiter, ok := m.Transport.(interface {
		WaitRead(context.Context, string, time.Duration) (bool, bool, error)
	})
	if !ok {
		return false, false, nil
	}
	used, notified, err = waiter.WaitRead(ctx, folder, maxWait)
	if used {
		m.d.mu.Lock()
		m.d.line.ReaderIdle = true
		m.d.line.IdleWaits++
		if notified {
			m.d.line.IdleNotifies++
		} else if err == nil {
			m.d.line.IdleAudits++
		}
		m.d.mu.Unlock()
	}
	if err != nil {
		m.d.mu.Lock()
		m.d.line.LastError = errorCode(err)
		m.d.mu.Unlock()
	}
	return used, notified, err
}

func (m *observedMail) WaitReadWake(ctx context.Context, folder string, maxWait time.Duration, wake <-chan struct{}) (used, notified, kicked bool, err error) {
	waiter, ok := m.Transport.(interface {
		WaitReadWake(context.Context, string, time.Duration, <-chan struct{}) (bool, bool, bool, error)
	})
	if !ok {
		return false, false, false, nil
	}
	used, notified, kicked, err = waiter.WaitReadWake(ctx, folder, maxWait, wake)
	if used {
		m.d.mu.Lock()
		m.d.line.ReaderIdle = true
		m.d.line.IdleWaits++
		switch {
		case notified:
			m.d.line.IdleNotifies++
		case kicked:
			m.d.line.IdleKicks++
		case err == nil:
			m.d.line.IdleAudits++
		}
		m.d.mu.Unlock()
	}
	if err != nil {
		m.d.mu.Lock()
		m.d.line.LastError = errorCode(err)
		m.d.mu.Unlock()
	}
	return used, notified, kicked, err
}

func (m *observedMail) RepairCleanup(ctx context.Context) error {
	if repair, ok := m.Transport.(interface{ RepairCleanup(context.Context) error }); ok {
		return repair.RepairCleanup(ctx)
	}
	return nil
}
func (m *observedMail) Page(c context.Context, f string, p mailbox.Cursor) (rows []mailbox.Message, next mailbox.Cursor, e error) {
	return m.pageObserved(c, f, p, nil)
}

func (m *observedMail) PageStream(c context.Context, f string, p mailbox.Cursor, emit func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error) {
	return m.pageObserved(c, f, p, emit)
}

func (m *observedMail) pageObserved(c context.Context, f string, p mailbox.Cursor, emit func(mailbox.Message) error) (rows []mailbox.Message, next mailbox.Cursor, e error) {
	// Diagnostic-only measurement of latency-critical wake to the next FETCH.
	// Long gaps matter; sampling below 200ms avoids a trace amplification loop.
	if m.d != nil && m.d.tracer != nil {
		if reporter, ok := m.Transport.(interface{ ReaderWakeToFetchDelay() time.Duration }); ok {
			if gap := reporter.ReaderWakeToFetchDelay(); gap >= 200*time.Millisecond {
				m.d.trace(traceEvent{Event: "reader_wake_to_fetch", DurationUS: gap.Microseconds()})
			}
		}
	}
	var id uint64
	if m.d.tracer != nil {
		id = m.d.tracer.operations.Add(1)
	}
	var beforeSelect, beforeFetch, beforeRetry uint64
	if stats, ok := m.Transport.(interface {
		ReaderCommandCounts() (uint64, uint64, uint64)
	}); ok {
		beforeSelect, beforeFetch, beforeRetry = stats.ReaderCommandCounts()
	}
	start := time.Now()
	binaryReader, hasBinaryReader := m.Transport.(interface{ ReaderBinaryFetchEnabled() bool })
	wasBinary := hasBinaryReader && binaryReader.ReaderBinaryFetchEnabled()
	streamed := false
	if reader, ok := m.Transport.(interface {
		PageStream(context.Context, string, mailbox.Cursor, func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error)
	}); ok && emit != nil {
		streamed = true
		rows, next, e = reader.PageStream(c, f, p, func(row mailbox.Message) error {
			m.d.packet("packet_rx", id, row)
			return emit(row)
		})
	} else {
		rows, next, e = m.Transport.Page(c, f, p)
		if emit != nil && e == nil {
			for _, row := range rows {
				if e = emit(row); e != nil {
					break
				}
			}
		}
	}
	if wasBinary && !binaryReader.ReaderBinaryFetchEnabled() {
		m.d.event("event=fetch_capability binary=false fallback=true")
	}
	elapsed := time.Since(start)
	var handoffs uint64
	if stats, ok := m.Transport.(interface{ ReaderHandoffs() uint64 }); ok {
		handoffs = stats.ReaderHandoffs()
	}
	m.d.mu.Lock()
	m.d.line.ReaderHandoffs = int64(handoffs)
	if stats, ok := m.Transport.(interface{ ReaderRepairs() uint64 }); ok {
		m.d.line.ReaderRepairs = stats.ReaderRepairs()
	}
	liveFolder := m.d.liveFolder
	m.d.mu.Unlock()
	live := liveFolder == "" || f == liveFolder
	// Empty successful tail polls are intentionally not emitted one-by-one. Also
	// suppress per-message packet_rx for fast successful maintenance scans of the
	// local send folder: gate14 traces made those self-owned records look like live
	// peer packets and multiplied trace volume during cleanup. Live receive rows,
	// errors and slow operations remain fully visible.
	tracePoll := e != nil || elapsed >= 100*time.Millisecond || live && len(rows) > 0
	if tracePoll {
		m.d.trace(traceEvent{Time: start.UTC().Format(time.RFC3339Nano), Event: "imap_begin", Name: "fetch_page", Operation: id})
		if live && !streamed {
			for _, row := range rows {
				m.d.packet("packet_rx", id, row)
			}
		}
		detailsMap := map[string]any{"rows": len(rows), "empty": len(rows) == 0, "live": live, "reader_handoffs": handoffs}
		if e != nil {
			detailsMap["reason"] = mailbox.FailureReason(e)
		}
		if live {
			if hasBinaryReader {
				detailsMap["binary_fetch"] = binaryReader.ReaderBinaryFetchEnabled()
			}
			if stats, ok := m.Transport.(interface {
				ReaderCommandCounts() (uint64, uint64, uint64)
			}); ok {
				a, b, d := stats.ReaderCommandCounts()
				detailsMap["select_commands"] = a - beforeSelect
				detailsMap["fetch_commands"] = b - beforeFetch
				detailsMap["partial_retries"] = d - beforeRetry
			}
		}
		details, _ := json.Marshal(detailsMap)
		m.d.trace(traceEvent{Event: "imap_end", Name: "fetch_page", Operation: id, Code: errorCode(e), DurationUS: elapsed.Microseconds(), Details: details})
	}
	m.d.mu.Lock()
	m.d.line.FetchPolls++
	if len(rows) == 0 {
		m.d.line.EmptyFetchPolls++
	}
	if e == nil {
		m.d.line.FetchMessages += int64(len(rows))
	}
	m.d.mu.Unlock()
	e = m.finishOperation("fetch_page", start, e)
	return
}

// PageRecovery is intentionally distinct from Page: startup journal recovery may
// inspect historical mailbox contents, but those rows are not live transport
// packets and must not be presented as packet_rx in the packet trace.
func (m *observedMail) PageRecovery(c context.Context, f string, p mailbox.Cursor) (rows []mailbox.Message, next mailbox.Cursor, e error) {
	e = m.operation("recovery_page", func(id uint64) error {
		rows, next, e = m.Transport.Page(c, f, p)
		for _, row := range rows {
			m.d.packet("recovery_rx", id, row)
		}
		return e
	})
	if e == nil {
		m.d.mu.Lock()
		m.d.line.FetchMessages += int64(len(rows))
		m.d.mu.Unlock()
	}
	return
}

func (m *observedMail) Tail(c context.Context, f string) (out mailbox.Cursor, e error) {
	e = m.operation("tail", func(id uint64) error {
		if t, ok := m.Transport.(interface {
			Tail(context.Context, string) (mailbox.Cursor, error)
		}); ok {
			out, e = t.Tail(c, f)
			return e
		}
		return mailbox.ErrLimit
	})
	return
}
func (m *observedMail) Recent(c context.Context, f string, limit int) (rows []mailbox.Message, tail mailbox.Cursor, e error) {
	e = m.operation("recent", func(id uint64) error {
		if r, ok := m.Transport.(interface {
			Recent(context.Context, string, int) ([]mailbox.Message, mailbox.Cursor, error)
		}); ok {
			rows, tail, e = r.Recent(c, f, limit)
			for _, row := range rows {
				m.d.packet("packet_rx", id, row)
			}
			return e
		}
		return mailbox.ErrLimit
	})
	if e == nil {
		m.d.mu.Lock()
		m.d.line.FetchMessages += int64(len(rows))
		m.d.mu.Unlock()
	}
	return
}
func (m *observedMail) Delete(c context.Context, r mailbox.Message) error {
	return m.operation("delete", func(id uint64) error {
		m.d.trace(traceEvent{Event: "gc_candidate", Operation: id, UID: r.UID, Validity: r.Validity})
		return m.Transport.Delete(c, r)
	})
}
func (m *observedMail) Absent(c context.Context, f string, v, id uint32) (absent bool, e error) {
	e = m.op("absence", func() error { absent, e = m.Transport.Absent(c, f, v, id); return e })
	return
}
func (m *observedMail) DeleteMany(c context.Context, rows []mailbox.Message) error {
	return m.operation("delete_batch", func(id uint64) error {
		for _, r := range rows {
			m.d.trace(traceEvent{Event: "gc_candidate", Operation: id, UID: r.UID, Validity: r.Validity})
		}
		if b, ok := m.Transport.(interface {
			DeleteMany(context.Context, []mailbox.Message) error
		}); ok {
			return b.DeleteMany(c, rows)
		}
		for _, r := range rows {
			if e := m.Transport.Delete(c, r); e != nil {
				return e
			}
		}
		return nil
	})
}
func (m *observedMail) DeleteObservedMany(c context.Context, rows []mailbox.Message) error {
	return m.operation("delete_observed_batch", func(id uint64) error {
		for _, r := range rows {
			m.d.trace(traceEvent{Event: "gc_candidate_observed", Operation: id, UID: r.UID, Validity: r.Validity})
		}
		if b, ok := m.Transport.(interface {
			DeleteObservedMany(context.Context, []mailbox.Message) error
		}); ok {
			return b.DeleteObservedMany(c, rows)
		}
		if b, ok := m.Transport.(interface {
			DeleteMany(context.Context, []mailbox.Message) error
		}); ok {
			return b.DeleteMany(c, rows)
		}
		for _, r := range rows {
			if e := m.Transport.Delete(c, r); e != nil {
				return e
			}
		}
		return nil
	})
}
func (m *observedMail) Present(c context.Context, f string, v uint32, ids []uint32) (out map[uint32]bool, e error) {
	e = m.operation("presence", func(id uint64) error {
		defer func() {
			if m.d.tracer != nil {
				unique := make(map[uint32]struct{}, len(ids))
				var minUID, maxUID uint32
				zero, duplicate := false, false
				for _, uid := range ids {
					if uid == 0 {
						zero = true
					}
					if _, exists := unique[uid]; exists {
						duplicate = true
					} else {
						unique[uid] = struct{}{}
					}
					if minUID == 0 || uid < minUID {
						minUID = uid
					}
					if uid > maxUID {
						maxUID = uid
					}
				}
				b, _ := json.Marshal(map[string]any{
					"requested_count": len(ids), "unique_count": len(unique), "present_count": len(out),
					"min_uid": minUID, "max_uid": maxUID, "zero_uid": zero, "duplicate_uid": duplicate,
				})
				m.d.trace(traceEvent{Event: "gc_presence", Operation: id, Validity: v, Details: b})
			}
		}()
		if b, ok := m.Transport.(interface {
			Present(context.Context, string, uint32, []uint32) (map[uint32]bool, error)
		}); ok {
			out, e = b.Present(c, f, v, ids)
			return e
		}
		out = map[uint32]bool{}
		for _, id := range ids {
			absent, err := m.Transport.Absent(c, f, v, id)
			if err != nil {
				return err
			}
			if !absent {
				out[id] = true
			}
		}
		return nil
	})
	return
}

func (d *diagnostic) beginConnect() {
	d.mu.Lock()
	d.line.Stage = "connecting"
	d.connectingAt = time.Now()
	d.line.InitialReadyMS = 0
	d.line.LastOperation = "dial_tls_login"
	d.line.LastConnectStage = ""
	d.line.LastConnectReason = ""
	d.mu.Unlock()
}
func (d *diagnostic) connected(start time.Time, err error) {
	ms := time.Since(start).Milliseconds()
	detail := mailbox.ConnectionDetail(err)
	d.mu.Lock()
	d.line.LastConnectStage = detail.Stage
	d.line.LastConnectReason = detail.Reason
	d.line.LastOperationMS = ms
	d.line.MaxOperationMS = max(d.line.MaxOperationMS, ms)
	d.line.Operations++
	if err != nil {
		d.line.LastError = errorCode(err)
	}
	d.mu.Unlock()
	if err == nil {
		d.event(fmt.Sprintf("event=imap_connected duration_ms=%d", ms))
	} else {
		if detail.Stage != "" {
			d.event(fmt.Sprintf("event=imap_connect_failed stage=%s reason=%s code=%s duration_ms=%d", detail.Stage, detail.Reason, errorCode(err), ms))
		}
	}
}

func errorOrigin(err error) string {
	if err != nil {
		for _, name := range []string{"control_append", "data_writer", "receive", "maintenance", "pump", "tcp_dial_complete", "tcp_open"} {
			if strings.Contains(err.Error(), name+":") {
				return name
			}
		}
	}
	return "transport"
}

func (m *observedMail) FetchCleanupCandidates(ctx context.Context, folder string, wanted map[[32]byte]bool) (rows []mailbox.Message, err error) {
	source, ok := m.Transport.(interface {
		FetchCleanupCandidates(context.Context, string, map[[32]byte]bool) ([]mailbox.Message, error)
	})
	if !ok {
		return nil, nil
	}
	err = m.operation("cleanup_fetch_exact", func(id uint64) error {
		rows, err = source.FetchCleanupCandidates(ctx, folder, wanted)
		for _, row := range rows {
			m.d.trace(traceEvent{Event: "gc_candidate_read", Operation: id, UID: row.UID, Validity: row.Validity, Bytes: len(row.MIME)})
		}
		return err
	})
	return
}

// Account echo samples are asynchronous transport observations, not provider
// NOOP latency or the application's Speedtest ping. Keep their age explicit.
func applyRTTDiagnostics(line *LineDiagnostic, l *lane, now time.Time) {
	line.RTTSampleAgeMS = -1
	line.RTTFresh = false
	line.RTTSampleKind = "CONTROL_ECHO"
	if l == nil {
		return
	}
	line.RTTMillis = l.rtt.Load()
	line.RTTSmoothedMillis = l.smoothedRTT.Load()
	line.RTTIdleMillis = l.idleRTT.Load()
	at := l.rttAt.Load()
	if at > 0 && now.UnixMilli() >= at {
		line.RTTSampleAgeMS = now.UnixMilli() - at
		line.RTTFresh = line.RTTSampleAgeMS <= 15000 && line.RTTMillis > 0
	}
}
