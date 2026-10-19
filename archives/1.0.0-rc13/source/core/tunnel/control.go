package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/session"
	"itom.local/core/wire"
)

const (
	controlBatch            = 16
	receiptFrameCounters    = 512 // ITH9 MailReceipt wire limit; do not change without a wire revision.
	receiptLocatorHardLimit = 2048
	receiptMinInterval      = 200 * time.Millisecond
)

type queuedControl struct {
	wire.Frame
	added          time.Time
	latestAt       time.Time // diagnostic only; newest unsent cumulative payload
	coalesced      bool
	quotaCoalesced bool
	quotaKey       wire.QuotaKey
}

// Each member has its own authenticated Noise counter. The outer lengths only
// delimit records and never authorize state changes or physical quota refunds.
func unpackControl(body []byte) ([][]byte, bool, error) {
	if bytes.HasPrefix(body, []byte("ITC1")) {
		return [][]byte{body[4:]}, true, nil
	}
	if !bytes.HasPrefix(body, []byte("ITB1")) {
		return [][]byte{body}, false, nil
	}
	body = body[4:]
	var records [][]byte
	for len(body) > 0 {
		if len(records) >= controlBatch || len(body) < 4 {
			return nil, true, wire.ErrLimit
		}
		n := int(binary.BigEndian.Uint32(body))
		body = body[4:]
		if n < wire.HeaderSize || n > 2*wire.HeaderSize+wire.FrameHeaderSize+wire.MaxControl+wire.TagSize || n > len(body) {
			return nil, true, wire.ErrFormat
		}
		records = append(records, body[:n])
		body = body[n:]
	}
	if len(records) == 0 {
		return nil, true, wire.ErrFormat
	}
	return records, true, nil
}
func transportPolicy() session.ControlPolicy {
	return session.ControlPolicy{Timeout: 90 * time.Second, RetryInterval: 8 * time.Second, MaxSends: 12}
}

func latencyControl(f wire.Frame) bool {
	// A conservative receipt/credit-only STATUS has no attempted DATA/final
	// to repair. It has ordinary flow-feedback urgency, preserving descriptor,
	// startup and repair service instead of putting every socket write first.
	if f.Type == wire.Status && f.Offset == 0 && len(f.Payload) == 33 && f.Payload[32] == 0 {
		return false
	}
	switch f.Type {
	case wire.Open, wire.OpenOK, wire.OpenError, wire.Ping, wire.Pong, wire.GrantUsage, wire.WriteGrant, wire.RecoveryFastWriteGrant, wire.GrantReconcile, wire.Status, wire.Nack, wire.GCProofRequest:
		return true
	case wire.Window:
		return f.Stream != 0 && f.Offset == runtimeStreamWindow
	default:
		return false
	}
}

// readerResponseWindow is intentionally narrower than latencyControl. Replies
// such as PONG/OPEN_OK must stay urgent on the writer, but kicking our own IMAP
// reader after sending a reply creates a reciprocal polling storm without
// shortening that reply's path. Only requests that are expected to cause fresh
// peer mail arm a short active receive window.
func readerResponseWindow(f wire.Frame) time.Duration {
	switch f.Type {
	case wire.Ping:
		return 450 * time.Millisecond
	case wire.GCProofRequest:
		return 650 * time.Millisecond
	case wire.Open:
		return 650 * time.Millisecond
	case wire.Window:
		if f.Stream != 0 && f.Offset == runtimeStreamWindow {
			return 300 * time.Millisecond
		}
	}
	return 0
}

func maintenanceControl(f wire.Frame) bool {
	switch f.Type {
	case wire.GrantRelease, wire.GCStatus, wire.GCProofRequest:
		return true
	default:
		return false
	}
}

func (l *lane) startControlWriter() {
	l.workers.Add(1)
	go func() {
		defer l.workers.Done()
		for {
			var first queuedControl
			// Usage/grant/open/liveness records gate application progress. Prefer
			// them over cleanup receipts when both are already queued; receipts
			// still have their own reserved slot and are collected into the same
			// bounded physical batch immediately afterwards.
			select {
			case first = <-l.urgentQueue:
			default:
				select {
				case first = <-l.receiptQueue:
				default:
					select {
					case first = <-l.controlQueue:
					default:
						select {
						case <-l.ctx.Done():
							return
						case first = <-l.urgentQueue:
						case first = <-l.receiptQueue:
						case first = <-l.controlQueue:
						case first = <-l.maintenanceQueue:
						}
					}
				}
			}
			first = l.resolveQuotaControl(l.resolveFlowControl(first))
			batch, live := l.collectControlBatch(first, controlBatchDelay(first.Frame))
			if !live {
				return
			}
			if e := l.writeControls(batch); e != nil {
				select {
				case l.writeErrors <- fmt.Errorf("control_append: %w", e):
				default:
				}
				return
			}
		}
	}()
}

func controlBatchDelay(f wire.Frame) time.Duration {
	if bulkReceiveFeedback(f) && !latencyControl(f) {
		return 20 * time.Millisecond
	}
	if f.Type == wire.Status || f.Type == wire.Nack {
		return 5 * time.Millisecond
	}
	if latencyControl(f) {
		return time.Millisecond
	}
	return 10 * time.Millisecond
}

func (l *lane) appendControlBatch(batch []queuedControl, f queuedControl, source int) []queuedControl {
	if source == 4 {
		f = l.resolveQuotaControl(f)
		return coalesceCleanupStatus(batch, f)
	}
	if source == 1 || source == 3 {
		f = l.resolveFlowControl(f)
	}
	if f.Type == wire.Window || f.Type == wire.Receipt || f.Type == wire.MailReceipt || f.Type == wire.Status {
		for i := range batch {
			if batch[i].Type == f.Type && batch[i].Stream == f.Stream {
				if f.Type == wire.Status {
					if merged, ok := wire.MergeProgressFrames(batch[i].Frame, f.Frame); ok {
						batch[i].Frame = merged
						batch[i].latestAt = f.latestAt
						return batch
					}
					// Never discard incompatible terminal state.
					return append(batch, f)
				}
				if source != 1 || f.Offset >= batch[i].Offset {
					batch[i] = f
				}
				return batch
			}
		}
	}
	return append(batch, f)
}

func (l *lane) readyControl(batchSize int) (queuedControl, int) {
	// A continuously ready urgent queue must still carry deletion receipts.
	if batchSize%4 == 0 {
		select {
		case f := <-l.receiptQueue:
			return f, 2
		default:
		}
	}
	select {
	case f := <-l.urgentQueue:
		return f, 1
	default:
	}
	select {
	case f := <-l.receiptQueue:
		return f, 2
	default:
	}
	select {
	case f := <-l.controlQueue:
		return f, 3
	default:
	}
	select {
	case f := <-l.maintenanceQueue:
		return f, 4
	default:
	}
	return queuedControl{}, 0
}

func (l *lane) collectControlBatch(first queuedControl, delay time.Duration) ([]queuedControl, bool) {
	batch := []queuedControl{first}
	deadline := time.Now().Add(delay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	shorten := func() {
		next := time.Now().Add(time.Millisecond)
		if next.Before(deadline) {
			deadline = next
			timer.Reset(max(time.Duration(0), time.Until(deadline)))
		}
	}
	// Coalescing may keep len(batch) constant under a continuously ready stream.
	// Bound dequeue work as well as output records so publication cannot starve.
	for consumed := 1; len(batch) < controlBatch && consumed < 4*controlBatch; consumed++ {
		if l.ctx.Err() != nil {
			return nil, false
		}
		// Drain ready frames before consulting the timer: a ready timer must not
		// randomly fragment an already queued parallel startup burst.
		if f, source := l.readyControl(len(batch)); source != 0 {
			batch = l.appendControlBatch(batch, f, source)
			if latencyControl(f.Frame) {
				shorten()
			}
			continue
		}
		select {
		case <-l.ctx.Done():
			return nil, false
		case <-timer.C:
			return batch, true
		case f := <-l.urgentQueue:
			batch = l.appendControlBatch(batch, f, 1)
			if latencyControl(f.Frame) {
				shorten()
			}
		case f := <-l.receiptQueue:
			batch = l.appendControlBatch(batch, f, 2)
		case f := <-l.controlQueue:
			batch = l.appendControlBatch(batch, f, 3)
		case f := <-l.maintenanceQueue:
			batch = l.appendControlBatch(batch, f, 4)
		}
	}
	return batch, true
}

// A PING waiting for the writer does not begin the peer response lifetime.
// The enqueue-side hint remains (it wakes IDLE promptly); once the physical
// APPEND is durably complete, refresh that same bounded search. Replies such as
// PONG must not create a reciprocal reader polling loop.
func (l *lane) refreshReaderAfterPingAppend(frames []queuedControl) {
	if l == nil || l.ctx.Err() != nil {
		return
	}
	for _, f := range frames {
		if f.Type == wire.Ping {
			l.armReaderHot(l.readerResponseBudget(readerResponseWindow(f.Frame)))
			return
		}
	}
}

func (l *lane) writeControls(frames []queuedControl) error {
	// Backpressure may recursively send a receipt or urgent control. A nested
	// completion must not advertise the outer writer as idle to fast selection.
	if l.controlWriting.Add(1) == 1 {
		l.controlWriteStarted.Store(time.Now().UnixNano())
	}
	defer func() {
		if l.controlWriting.Add(-1) == 0 {
			l.controlWriteStarted.Store(0)
		}
	}()
	repairs := 0
	for {
		l.mu.Lock()
		held, e := l.control.Held(l.account.ID, l.user.ID)
		count := 0
		for _, o := range l.out {
			count += o.records
		}
		if e == nil {
			l.controlDebt.Store(held.Messages)
			bulkLimit := uint64(64)
			if l.side {
				bulkLimit = 80
			}
			l.bulkControlBlocked.Store(held.Messages >= bulkLimit || count >= 288)
		}
		onlyAck := true
		critical := false
		for _, f := range frames {
			if latencyControl(f.Frame) || maintenanceControl(f.Frame) {
				critical = true
			}
			if f.Type != wire.MailReceipt {
				onlyAck = false
			}
		}
		if e == nil && held.Messages >= 80 && critical && !onlyAck {
			var priority, ordinary []queuedControl
			for _, f := range frames {
				if latencyControl(f.Frame) || maintenanceControl(f.Frame) || f.Type == wire.MailReceipt {
					priority = append(priority, f)
				} else {
					ordinary = append(ordinary, f)
				}
			}
			if len(ordinary) > 0 && len(priority) > 0 {
				l.mu.Unlock()
				if err := l.writeControls(priority); err != nil {
					return err
				}
				frames = ordinary
				continue
			}
		}
		// Preserve slots for acknowledgements so two full senders can still drain.
		messageLimit, recordLimit := uint64(80), 256
		if critical {
			messageLimit, recordLimit = 96, 320
		}
		if e == nil && ((held.Messages >= messageLimit || count+len(frames) > recordLimit) && !onlyAck || held.Messages >= 112) {
			l.mu.Unlock()
			// A coalesced physical receipt must retain its independent service
			// even when the remainder of its batch is backpressured.
			if !onlyAck {
				remaining := make([]queuedControl, 0, len(frames))
				for _, f := range frames {
					if f.Type == wire.MailReceipt {
						if e = l.writeControls([]queuedControl{f}); e != nil {
							return e
						}
					} else {
						remaining = append(remaining, f)
					}
				}
				if len(remaining) != len(frames) {
					frames = remaining
					continue
				}
			}
			urgent, ackQueue := l.urgentQueue, l.receiptQueue
			if critical || onlyAck {
				urgent = nil
			}
			if onlyAck {
				ackQueue = nil
			}
			select {
			case urgentFrame := <-urgent:
				if e = l.writeControls([]queuedControl{urgentFrame}); e != nil {
					return e
				}
			case ack := <-ackQueue:
				if e = l.writeControls([]queuedControl{ack}); e != nil {
					return e
				}
			case <-time.After(50 * time.Millisecond):
			case <-l.ctx.Done():
				return l.ctx.Err()
			}
			continue
		}
		if e != nil {
			l.mu.Unlock()
			return e
		}
		l.mu.Unlock()
		if l.diag != nil {
			l.diag.mu.Lock()
			l.diag.line.ControlRecords += int64(len(frames))
			l.diag.mu.Unlock()
		}
		if io := physicalMail(l.mail); io != nil && !io.controlClient().Ready() {
			if repairs >= 2 {
				return errors.New("CONTROL_REPAIR_EXHAUSTED")
			}
			repairs++
			if err := io.repairControl(l.ctx); err != nil {
				return err
			}
		}
		body := []byte("ITB1")
		counters := []uint64{}
		hasAck := false
		for _, f := range frames {
			packet, err := l.cipher.Seal(f.Frame)
			if err != nil {
				return err
			}
			h, err := wire.ParseHeader(packet[:wire.HeaderSize])
			if err != nil {
				return err
			}
			l.traceFrame("frame_sealed", f.Frame, &h.Counter)
			waited := time.Since(f.added)
			// Keep latency-critical control visible; routine control is traced only
			// when it actually waited long enough to matter for application latency.
			if (latencyControl(f.Frame) && !(f.Type == wire.Status && f.Offset == 0)) || waited >= 100*time.Millisecond {
				l.diag.trace(traceEvent{Event: "control_wait", Name: f.Type.String(), Counter: &h.Counter, Stream: f.Stream, DurationUS: waited.Microseconds()})
				if f.Type == wire.Status {
					// Keep the age of the *current* coalesced ACK distinct from
					// the age of its first queue token. Neither time is proof.
					latest := f.latestAt
					if latest.IsZero() {
						latest = f.added
					}
					l.diag.trace(traceEvent{Event: "control_latest_wait", Name: "STATUS", Counter: &h.Counter, Stream: f.Stream, DurationUS: max(int64(0), time.Since(latest).Microseconds())})
				}
			}
			body = binary.BigEndian.AppendUint32(body, uint32(len(packet)))
			body = append(body, packet...)
			if f.Type == wire.MailReceipt {
				hasAck = true
			} else {
				counters = append(counters, h.Counter)
			}
		}
		if l.diag != nil {
			var age int64
			for _, f := range frames {
				age = max(age, time.Since(f.added).Milliseconds())
			}
			l.diag.mu.Lock()
			l.diag.line.ControlWaitMS = age
			l.diag.line.MaxControlWaitMS = max(l.diag.line.MaxControlWaitMS, age)
			l.diag.mu.Unlock()
		}
		appendStarted := time.Now()
		ref, e := appendDurableControl(l.ctx, l.mail, l.control, journal.Scope{Account: l.account.ID, User: l.user.ID, Session: l.cipher.Context().Session}, l.sendFolder, body)
		if l.diag != nil && l.diag.tracer != nil {
			kinds := map[string]int{}
			streams := map[uint64]bool{}
			for _, f := range frames {
				kinds[f.Type.String()]++
				if f.Stream != 0 {
					streams[f.Stream] = true
				}
			}
			l.traceBatchStage("control_batch_append", appendStarted, errorCode(e), map[string]any{"records": len(frames), "streams": len(streams), "decoded_bytes": len(body), "kinds": kinds})
		}
		// An uncertain physical copy can already be visible to the peer. Keep
		// its counters/ref discoverable for real receipt and cleanup proofs.
		if e != nil && ref.ID != 0 && repairableControlFailure(e) {
			attempt, lookupErr := l.control.GetAttempt(ref)
			if lookupErr != nil {
				return errors.Join(e, lookupErr)
			}
			if attempt.State != journal.Unknown && attempt.State != journal.Committed {
				return e
			}
			l.mu.Lock()
			l.registerOutgoingLocked(outgoing{ref: ref, counters: counters, records: len(counters), ack: len(counters) == 0})
			l.mu.Unlock()
			if io := physicalMail(l.mail); io != nil && repairs < 2 && l.ctx.Err() == nil {
				repairs++
				if err := io.repairControl(l.ctx); err == nil {
					if l.diag != nil {
						l.diag.event("event=control_socket_repaired uncertain_debt_retained=true")
					}
					continue // fresh Seal counters + independently charged attempt
				}
			}
		}
		if e == nil {
			l.refreshReaderAfterPingAppend(frames)
			l.mu.Lock()
			if hasAck {
				for _, old := range l.out {
					if old.ack {
						l.obsolete[old.ref] = true
					}
				}
			}
			l.registerOutgoingLocked(outgoing{ref: ref, counters: counters, records: len(counters), ack: len(counters) == 0})
			l.mu.Unlock()
		}
		return e
	}
}
func (l *lane) acceptRecord(m mailbox.Message, body []byte, control bool) error {
	if len(body) < wire.HeaderSize {
		return nil
	}
	h, e := wire.ParseHeader(body[:wire.HeaderSize])
	if e != nil || h.Session != l.cipher.Context().Session {
		return nil
	}
	var f wire.Frame
	if control {
		f, e = l.cipher.OpenControl(body)
	} else if !l.side {
		f, e = l.cipher.Open(body)
	} else {
		return nil
	}
	if e != nil {
		l.diag.trace(traceEvent{Event: "record_rejected", Code: errorCode(e), Counter: &h.Counter, UID: m.UID, Validity: m.Validity})
		return nil
	}
	l.traceFrame("frame_rx", f, &h.Counter)
	if l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.ReceivedRecords++
		switch f.Type {
		case wire.Window:
			l.diag.line.WindowReceived++
			l.diag.line.MaxWindowReceived = max(l.diag.line.MaxWindowReceived, f.Offset)
		case wire.Receipt:
			l.diag.line.ReceiptReceived++
		}
		l.diag.mu.Unlock()
	}
	// Keep the physical locator window bounded, but do not confuse the ITH9
	// 512-counter receipt *frame* limit with the amount of transient locator debt
	// a busy carrier may safely retain. Gate14 failed entire lanes at 512 even
	// though cleanup only needed a little more time to prove deletion. Locators
	// are stored without MIME payloads below, so a 2048-entry cushion is small and
	// remains fail-closed if the peer never consumes receipts at all.
	if f.Type != wire.MailReceipt {
		if _, exists := l.known[h.Counter]; !exists && len(l.known) >= receiptLocatorHardLimit {
			return errors.New("RECEIPT_WINDOW_LIMIT")
		}
	}
	if e = l.process(f); e != nil {
		return e
	}
	if !l.side && f.Type == wire.Data && l.accountView != nil {
		l.accountView.download.Add(uint64(len(f.Payload)))
	}
	// Any freshly authenticated, replay-checked peer record proves that this
	// carrier still has end-to-end transport progress. Gate11 only refreshed
	// lastRX here, leaving lastProgress unchanged; the 25 s watchdog therefore
	// disconnected healthy control-only/mostly-download lanes even while PONG,
	// STATUS, receipts or DATA were arriving. Keep the diagnostic RX timestamp
	// and the transport-health timestamp in lockstep after successful processing.
	l.markRemoteProgress()
	if f.Type != wire.MailReceipt {
		// Presence proof needs only the immutable physical locator. Retaining MIME
		// here multiplied the memory cost of the receipt window for no correctness
		// benefit and made a larger transient cushion unnecessarily expensive.
		l.known[h.Counter] = mailbox.Message{Folder: m.Folder, Validity: m.Validity, UID: m.UID}
		l.ackDirty = true
	}
	return nil
}
func (l *lane) sendReceipt() error {
	if len(l.known) == 0 {
		return nil
	}
	now := time.Now()
	if l.ackDirty {
		// A busy FETCH loop used to enqueue a full cumulative receipt after every
		// page. Coalesce those bursts while keeping acknowledgement latency far below
		// one provider RTT in normal field conditions.
		if !l.lastReceipt.IsZero() && now.Sub(l.lastReceipt) < receiptMinInterval {
			return nil
		}
	} else if now.Sub(l.lastReceipt) < 5*time.Second {
		return nil
	}
	counters := make([]uint64, 0, len(l.known))
	for n := range l.known {
		counters = append(counters, n)
	}
	sort.Slice(counters, func(i, j int) bool { return counters[i] < counters[j] })
	// MailReceipt itself remains exactly ITH9-compatible. If locator debt exceeds
	// one wire frame, repeatedly acknowledge the oldest 512 until sender cleanup
	// makes their physical messages disappear; presence cleanup then advances the
	// window naturally to later counters.
	if len(counters) > receiptFrameCounters {
		counters = counters[:receiptFrameCounters]
	}
	if e := l.controlSend(wire.MailReceived{Counters: counters}); e != nil {
		return e
	}
	l.ackDirty = false
	l.lastReceipt = now
	return nil
}

// Used by the DATA writer before claiming a one-shot Job. It must never retry
// an attempted APPEND. Waiting here leaves the owner free to receive and GC.
func (l *lane) waitLocalGrant(bytes uint64) (journal.Grant, error) {
	return l.waitLocalGrantContextClass(l.ctx, bytes, false)
}

func (l *lane) waitLocalGrantContext(ctx context.Context, bytes uint64) (journal.Grant, error) {
	return l.waitLocalGrantContextClass(ctx, bytes, false)
}

func (l *lane) waitLocalGrantContextClass(ctx context.Context, bytes uint64, fast bool) (journal.Grant, error) {
	delay := 25 * time.Millisecond
	if fast {
		delay = 5 * time.Millisecond
	}
	for {
		g, e := l.tryLocalGrant(bytes, fast)
		if !errors.Is(e, journal.ErrLimit) {
			return g, e
		}
		if err := pause(ctx, delay); err != nil {
			return journal.Grant{}, err
		}
	}
}

// tryLocalGrant reserves durable capacity without waiting behind one carrier.
func (l *lane) tryLocalGrant(bytes uint64, fast bool) (journal.Grant, error) {
	l.localGrantMu.Lock()
	if !fast {
		held, heldErr := l.data.Held(l.account.ID, l.user.ID)
		if heldErr != nil {
			l.localGrantMu.Unlock()
			return journal.Grant{}, heldErr
		}
		if l.diag != nil {
			l.diag.mu.Lock()
			l.diag.line.PeakDataHeldBytes = max(l.diag.line.PeakDataHeldBytes, held.Bytes)
			l.diag.line.PeakDataHeldMessages = max(l.diag.line.PeakDataHeldMessages, held.Messages)
			l.diag.mu.Unlock()
		}
		profile, profileErr := l.data.Limit(l.account.ID, l.user.ID)
		if profileErr != nil {
			l.localGrantMu.Unlock()
			return journal.Grant{}, profileErr
		}
		reserveBytes, reserveMessages := uint64(localFastReserveBytes), uint64(0)
		originBlocked := false
		if l.side && l.stripe != nil {
			current := wire.ID{}
			if l.cipher != nil {
				current = l.cipher.Context().Session
			}
			usage, err := l.data.UsageByOrigin(l.account.ID, l.user.ID, current)
			if l.cipher == nil {
				usage.HistoricalLocal, usage.HistoricalRemote = pool.Amount{}, pool.Amount{}
			}
			if err != nil {
				l.localGrantMu.Unlock()
				return journal.Grant{}, err
			}
			localShare, _, err := recoveredDirectionalShares(profile, usage)
			if err != nil {
				l.localGrantMu.Unlock()
				return journal.Grant{}, err
			}
			reserveBytes, _ = recoveryFastReserves(profile, usage)
			originBlocked = localShare.Bytes < reserveBytes || localShare.Messages < 1 || usage.Local.Bytes+bytes > localShare.Bytes-reserveBytes || usage.Local.Messages+1 > localShare.Messages-1
			reserveMessages = 1
		}
		if originBlocked || held.Bytes+bytes > profile.Bytes-reserveBytes || held.Messages+1 > profile.Messages-reserveMessages {
			if l.diag != nil {
				l.diag.mu.Lock()
				l.diag.line.LocalGrantHeadroomWaits++
				l.diag.mu.Unlock()
			}
			l.localGrantMu.Unlock()
			return journal.Grant{}, journal.ErrLimit
		}
	}
	g, e := l.data.Issue(journal.Scope{Account: l.account.ID, User: l.user.ID, Session: l.cipher.Context().Session}, pool.Amount{Bytes: bytes, Messages: 1})
	if errors.Is(e, journal.ErrLimit) && l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.LocalGrantLimitWaits++
		l.diag.mu.Unlock()
	}
	l.localGrantMu.Unlock()
	return g, e
}

// Receipts may arrive while APPEND is still waiting for its tagged completion.
// Reconcile that race without holding the actor mutex over network I/O.
func (l *lane) registerOutgoing(o outgoing) {
	l.mu.Lock()
	l.registerOutgoingLocked(o)
	l.mu.Unlock()
	if o.owner != nil && l.stripe != nil {
		l.stripe.rememberLocal(l.account.ID, o.ref, o.owner, o.attempts)
	}
}
func (l *lane) registerOutgoingLocked(o outgoing) {
	pending := o.counters[:0]
	for _, counter := range o.counters {
		if !l.mailReceipts[counter] {
			pending = append(pending, counter)
		}
	}
	o.counters = pending
	if !o.ack && len(pending) == 0 {
		l.obsolete[o.ref] = true
	}
	l.observeDownloadDeliveryLocked(&o)
	l.out = append(l.out, o)
	l.refreshUnreceivedLocalLocked()
}

// Receipt lag is an advisory reader-backlog signal, never a quota or cleanup
// proof. A fast reply should avoid a carrier with whole DATA bodies still
// awaiting the peer reader, even when the APPEND socket is already idle.
func (l *lane) refreshUnreceivedLocalLocked() {
	var n, bytes, knownBodies, newestKnown int64
	if l.data != nil {
		store := l.data.StoreID()
		for _, o := range l.out {
			if o.ref.Grant.Store == store && len(o.counters) > 0 {
				n += int64(len(o.counters))
				bytes += o.usefulBytes
				if o.receiptEligible {
					knownBodies++
					newestKnown = max(newestKnown, o.receiptStarted.UnixNano())
				}
			}
		}
	}
	l.peerReadNewestPendingAt.Store(newestKnown)
	l.peerReadPendingBodies.Store(knownBodies)
	l.unreceivedLocalRecords.Store(n)
	l.unreceivedLocalBytes.Store(bytes)
	if l.stripe != nil {
		l.stripe.signal()
	}
}

// Receipt feedback affects pacing only; existing physical GC still exclusively
// releases durable quota. Under l.mu, use only a group atomic to avoid reversing
// the group selection -> lane lock order. Count even a receipt arriving before
// tagged APPEND completes, but never count it twice.
func (l *lane) observeDownloadDeliveryLocked(o *outgoing) {
	if !l.side || l.stripe == nil || o.rateCounted || o.usefulBytes <= 0 || len(o.counters) != 0 {
		return
	}
	o.rateCounted = true
	l.stripe.downloadDelivered.Add(o.usefulBytes)
	if o.receiptEligible {
		l.observePeerReadService(o.usefulBytes, o.receiptStarted, time.Now())
	}
}
