package datapath

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"itom.local/core/flow"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"sync/atomic"
	"time"
)

// Job is a one-use immutable worker handoff; copies share the same claim bit.
// Engine.Begin sets the flow attempt; only these writers authorize physical I/O.
type Job struct {
	generation uint64
	attempt    flow.Attempt
	frame      wire.Frame
	ctx        context.Context
	claimed    *atomic.Bool
	queuedAt   time.Time
	bulk       bool
}
type Completion struct {
	Generation, Attempt uint64
	Outcome             flow.Outcome
	Usage               *wire.UsageReport
	Local               *journal.AttemptRef
	Err                 error
	// NonFatal permits a definitive pre-APPEND retry after successful cancellation,
	// or an unknown APPEND whose durable accounting remains charged. The byte
	// engine requeues NotCommitted; authenticated probe/NACK repairs CommitUnknown.
	NonFatal bool
	// RetryWake parks an unstarted NotCommitted item in the owner until its
	// admission predicate changes. It owns no physical slot or worker goroutine.
	RetryWake <-chan struct{}
}
type Appender interface {
	Append(context.Context, string, []byte) error
}

func (j *Job) Attempt() flow.Attempt { return j.attempt }

// Interactive is captured by the owner at handoff. TCP read fragmentation must
// not give a sustained byte stream the short-request scheduling reserve.
func (j *Job) Interactive() bool {
	return j != nil && !j.bulk && j.attempt.PayloadBytes <= InteractiveDataThreshold
}

// QueueWait reports time spent between Pump admission and physical batch
// execution. It bounds batching residence and is also diagnostic metadata; it never
// changes protocol offsets or physical accounting.
func (j *Job) QueueWait(now time.Time) time.Duration {
	if j == nil || j.queuedAt.IsZero() || now.Before(j.queuedAt) {
		return 0
	}
	return now.Sub(j.queuedAt)
}

// RejectBeforeCommit consumes an unstarted one-shot job without allocating a
// Noise nonce or authorizing physical I/O. Batch writers use it to guarantee
// exactly one completion for every member when preparation fails part-way.
func (j *Job) RejectBeforeCommit(err error) Completion {
	r, ok := j.claim()
	if ok {
		clear(j.frame.Payload)
		r.Err = err
	}
	return r
}

// SealForBatch consumes this one-shot worker handoff and encrypts its DATA frame
// exactly once. It does not perform or authorize physical I/O. The returned
// Completion remains NotCommitted until the batch writer durably records one
// physical attempt and performs APPEND.
func (j *Job) SealForBatch(cipher *secure.Epoch) (Completion, []byte, uint64) {
	r, ok := j.claim()
	if !ok {
		return r, nil, 0
	}
	defer clear(j.frame.Payload)
	if cipher == nil {
		r.Err = ErrState
		return r, nil, 0
	}
	packet, err := cipher.Seal(j.frame)
	if err != nil {
		r.Err = err
		return r, nil, 0
	}
	h, err := wire.ParseHeader(packet[:wire.HeaderSize])
	if err != nil {
		clear(packet)
		r.Err = err
		return r, nil, 0
	}
	return r, packet, h.Counter
}

// FrameInfo is metadata only. No observer receives mutable or plaintext payload.
func (j *Job) FrameInfo() wire.Frame { f := j.frame; f.Payload = nil; return f }
func (j *Job) claim() (Completion, bool) {
	r := Completion{Generation: j.generation, Attempt: j.attempt.ID, Outcome: flow.NotCommitted}
	if j.claimed == nil || !j.claimed.CompareAndSwap(false, true) {
		r.Err = ErrState
		return r, false
	}
	if j.ctx.Err() != nil {
		r.Err = j.ctx.Err()
		return r, false
	}
	return r, true
}

// Size does not consume a Noise nonce or grant. It exactly matches single-frame
// Seal and MIME framing. A worker verifies it again before any durable spend.
func Size(f wire.Frame, key *wire.QuotaKey) (uint64, error) {
	if err := f.Validate(); err != nil {
		return 0, err
	}
	bodyBytes := 2*wire.HeaderSize + wire.FrameHeaderSize + len(f.Payload) + wire.TagSize
	if key != nil {
		binding, err := wire.BindPhysical(*key, 1, wire.ID{1}, []byte{0})
		if err != nil {
			return 0, err
		}
		bodyBytes += len(binding) - 1
	}
	return mailbox.EncodedSize(bodyBytes)
}
func (j *Job) WriteRemote(w *wallet.Wallet, key wire.QuotaKey, folder string, cipher *secure.Epoch, c Appender) Completion {
	r, ok := j.claim()
	if !ok {
		return r
	}
	defer clear(j.frame.Payload)
	binding := cipher.Context()
	if key.Account != j.attempt.Account || key.Session != w.Session() || key.Session != binding.Session || key.User != binding.User {
		r.Err = ErrState
		return r
	}
	packet, err := cipher.Seal(j.frame)
	if err != nil {
		r.Err = err
		return r
	}
	defer clear(packet)
	dummy, err := wire.BindPhysical(key, 1, wire.ID{1}, packet)
	if err != nil {
		r.Err = err
		return r
	}
	encoded, err := mailbox.EncodedSize(len(dummy))
	clear(dummy)
	if err != nil || encoded != j.attempt.EncodedBytes {
		r.Err = ErrState
		return r
	}
	if j.ctx.Err() != nil {
		r.Err = j.ctx.Err()
		return r
	}
	usage, body, err := w.Spend(key, folder, packet, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if err != nil {
		r.Err = err
		return r
	}
	r.Usage = &usage // Must be retransmitted via the authenticated control lane.
	defer clear(body)
	mime, err := mailbox.Encode(body)
	if err != nil {
		r.Err = err
		return r
	}
	defer clear(mime)
	// After durable Spend, a cancellation still retains the wallet's charge.
	// The logical job can be retried, but no caller may reuse this physical permit.
	if j.ctx.Err() != nil {
		r.Err = j.ctx.Err()
		return r
	}
	err = c.Append(j.ctx, folder, mime)
	r.Outcome = flow.Committed
	if err != nil {
		r.Outcome = flow.CommitUnknown
	}
	r.Err = err
	return r
}
func (j *Job) WriteLocal(store *journal.Store, grant journal.GrantRef, folder string, cipher *secure.Epoch, c Appender) Completion {
	r, ok := j.claim()
	if !ok {
		return r
	}
	defer clear(j.frame.Payload)
	binding := cipher.Context()
	if grant.Scope.Account != j.attempt.Account || grant.Scope.Session != binding.Session || grant.Scope.User != binding.User {
		r.Err = ErrState
		return r
	}
	packet, err := cipher.Seal(j.frame)
	if err != nil {
		r.Err = err
		return r
	}
	defer clear(packet)
	mime, err := mailbox.Encode(packet)
	if err != nil || uint64(len(mime)) != j.attempt.EncodedBytes {
		r.Err = ErrState
		return r
	}
	defer clear(mime)
	var batch wire.ID
	if _, err = rand.Read(batch[:]); err != nil {
		r.Err = err
		return r
	}
	if j.ctx.Err() != nil {
		r.Err = j.ctx.Err()
		return r
	}
	a, err := store.Prepare(grant, folder, batch, uint64(len(mime)), packet)
	if err != nil {
		r.Err = err
		return r
	}
	r.Local = &a.Ref
	if j.ctx.Err() != nil {
		r.Err = errors.Join(j.ctx.Err(), store.CancelPrepared(a.Ref))
		return r
	}
	if _, err = store.Start(a.Ref); err != nil {
		r.Err = err
		return r
	}
	// Start is durable. Any failure after handing off APPEND is uncertain.
	err = c.Append(j.ctx, folder, mime)
	outcome := journal.Accepted
	r.Outcome = flow.Committed
	if err != nil {
		outcome = journal.Uncertain
		r.Outcome = flow.CommitUnknown
	}
	r.Err = errors.Join(err, store.Complete(a.Ref, outcome))
	return r
}

// OpenRemote requires an authenticated descriptor. Clear ITQ1 markers cannot
// authorize delivery or cleanup by themselves. The encrypted record is then
// checked against this session's Noise direction, replay window and binding.
func OpenRemote(raw []byte, report wire.UsageReport, cipher *secure.Epoch) (wire.Frame, error) {
	if _, err := wire.EncodeControl(0, report); err != nil || report.Attempt == nil {
		return wire.Frame{}, ErrState
	}
	binding := cipher.Context()
	if report.Key.Session != binding.Session || report.Key.User != binding.User {
		return wire.Frame{}, ErrState
	}
	d := report.Attempt
	if len(raw) != int(d.EncodedBytes) {
		return wire.Frame{}, ErrState
	}
	body, err := mailbox.Decode(raw)
	if err != nil {
		return wire.Frame{}, err
	}
	if uint64(len(body)) != d.DecodedBytes || sha256.Sum256(body) != d.Digest || !wire.CheckPhysical(body, report.Key, report.Total.Messages, d.Batch) {
		return wire.Frame{}, ErrState
	}
	// Encode a one-byte placeholder to calculate the bounded variable key prefix.
	prefix, err := wire.BindPhysical(report.Key, report.Total.Messages, d.Batch, []byte{0})
	if err != nil || len(body) < len(prefix) {
		return wire.Frame{}, ErrState
	}
	return cipher.Open(body[len(prefix)-1:])
}

// BatchValidationError retains the generic fail-closed DATA_STATE sentinel while
// exporting only a fixed validation stage. No hashes, payloads, account names or
// provider text are exposed to the diagnostic ring.
type BatchValidationError struct{ Stage string }

func (e *BatchValidationError) Error() string { return "DATA_STATE_" + e.Stage }
func (e *BatchValidationError) Unwrap() error { return ErrState }

// ValidationStage is a stable, low-cardinality field for failed DATA checks.
func ValidationStage(err error) string {
	var e *BatchValidationError
	if errors.As(err, &e) {
		return e.Stage
	}
	return "unknown"
}

// OpenRemoteBatch preserves the original MIME descriptor checks for callers
// with a canonical physical envelope. Decoded bytes are owned/cleared here.
func OpenRemoteBatch(raw []byte, report wire.UsageReport, cipher *secure.Epoch) ([]wire.Frame, error) {
	if _, err := wire.EncodeControl(0, report); err != nil || report.Attempt == nil || cipher == nil {
		return nil, &BatchValidationError{Stage: "descriptor"}
	}
	binding := cipher.Context()
	if report.Key.Session != binding.Session || report.Key.User != binding.User {
		return nil, &BatchValidationError{Stage: "binding"}
	}
	if len(raw) != int(report.Attempt.EncodedBytes) {
		return nil, &BatchValidationError{Stage: "encoded_size"}
	}
	body, err := mailbox.Decode(raw)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	return OpenRemoteBatchBody(body, uint64(len(raw)), report, cipher)
}

// OpenRemoteBatchBody verifies a previously decoded physical IMAP message
// without making a redundant base64 MIME document and decoding it again.
// Callers MUST provide the canonical charged MIME size derived from the
// original bounded physical envelope, not the untrusted RFC822.SIZE alone.
// The provided decoded body remains caller-owned and is never modified.
// It is NOT authorization to deliver data: digest, session, grant, batch
// binding and per-frame AEAD/replay checks below all remain mandatory.
func OpenRemoteBatchBody(body []byte, canonicalBytes uint64, report wire.UsageReport, cipher *secure.Epoch) ([]wire.Frame, error) {
	if _, err := wire.EncodeControl(0, report); err != nil || report.Attempt == nil || cipher == nil {
		return nil, &BatchValidationError{Stage: "descriptor"}
	}
	binding := cipher.Context()
	if report.Key.Session != binding.Session || report.Key.User != binding.User {
		return nil, &BatchValidationError{Stage: "binding"}
	}
	d := report.Attempt
	if canonicalBytes != d.EncodedBytes {
		return nil, &BatchValidationError{Stage: "encoded_size"}
	}
	if uint64(len(body)) != d.DecodedBytes {
		return nil, &BatchValidationError{Stage: "decoded_size"}
	}
	var payload []byte
	var err error
	if bytes.HasPrefix(body, []byte(wire.DataEnvelopeMagic)) {
		// This validates the complete immutable descriptor, including its whole-
		// BODY digest, quota identity, ordinal and batch. Rehashing through a
		// separate CheckPhysical pass adds no independent evidence.
		payload, err = wire.DataEnvelopePayload(body, report)
		if errors.Is(err, wire.ErrDataEnvelopeDigest) {
			return nil, &BatchValidationError{Stage: "digest"}
		}
		if err != nil {
			return nil, &BatchValidationError{Stage: "physical_binding"}
		}
	} else {
		if sha256.Sum256(body) != d.Digest {
			return nil, &BatchValidationError{Stage: "digest"}
		}
		if !wire.CheckPhysical(body, report.Key, report.Total.Messages, d.Batch) {
			return nil, &BatchValidationError{Stage: "physical_binding"}
		}
		var prefix []byte
		prefix, err = wire.BindPhysical(report.Key, report.Total.Messages, d.Batch, []byte{0})
		if err == nil && len(body) >= len(prefix) {
			payload = body[len(prefix)-1:]
		} else {
			err = ErrState
		}
	}
	if err != nil {
		return nil, &BatchValidationError{Stage: "prefix"}
	}
	_, packets, err := wire.UnpackDataBatch(payload)
	if err != nil {
		return nil, err
	}
	frames := make([]wire.Frame, 0, len(packets))
	for _, packet := range packets {
		f, openErr := cipher.Open(packet)
		if openErr != nil {
			for i := range frames {
				clear(frames[i].Payload)
			}
			return nil, openErr
		}
		if f.Type != wire.Data {
			clear(f.Payload)
			for i := range frames {
				clear(frames[i].Payload)
			}
			return nil, &BatchValidationError{Stage: "unexpected_frame"}
		}
		frames = append(frames, f)
	}
	return frames, nil
}
