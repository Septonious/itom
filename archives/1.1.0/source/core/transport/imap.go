package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrUnknown = errors.New("V2_IMAP_OUTCOME_UNKNOWN")
var ErrRefused = errors.New("V2_IMAP_TAGGED_REFUSAL")

// Only command admission is transient on the pending-tag edge. Inventory,
// literal and response bounds also match ErrCapacity but require explicit
// handling; they must not become an unbounded discovery retry loop.
var errCommandCapacity = fmt.Errorf("%w: pending IMAP command", ErrCapacity)

type Body struct {
	sequenceVersion uint64 // parser position of this BODY, never authority
	UID, Sequence   uint32
	Size            uint32 // RFC822.SIZE discovery hint, never accounting authority
	SizeKnown       bool
	Bytes           []byte
	Flags           []string
}
type Result struct {
	Bodies []Body
	Err    error
}
type operation struct {
	watchdog     *time.Timer // stopped under Executor.mu on every terminal path
	fetchTurn    *fetchRequest
	class        commandClass
	started      time.Time
	done         chan struct{}
	continuation chan struct{}
	result       Result
	bytes        int
	fetch        bool
	metadata     bool
	flags        bool
	first, last  uint32
	uids         map[uint32]bool
	requested    map[uint32]bool
}

// Command is a completion handle. Wait cancellation detaches that waiter; it
// does not cancel a shared socket, erase a tag or retry an ambiguous APPEND.
type Command struct{ op *operation }

func (c *Command) Wait(ctx context.Context) ([]Body, error) {
	select {
	case <-c.op.done:
		return c.op.result.Bodies, c.op.result.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type IMAPLimits struct {
	Pending, LineBytes, LiteralBytes, ResponseBytes int
	ProgressTimeout                                 time.Duration
}

func DefaultIMAPLimits() IMAPLimits {
	return IMAPLimits{Pending: 16, LineBytes: 8 << 10, LiteralBytes: 2 << 20, ResponseBytes: 4 << 20, ProgressTimeout: 30 * time.Second}
}

// Executor pins one selected mailbox. It owns the only parser and serializes
// bytes on the wire, but releases the writer before tagged completion. APPEND
// and bounded UID FETCH may overlap. Internal singleton BODY and disjoint FLAGS
// reads may share the parser; inventory, absence proof, grouped bodies and the
// public FETCH API retain exclusive admission. IDLE owns the writer until DONE and
// its tag have drained. Mailbox selection changes require a new executor.
type Executor struct {
	literalPlus      bool
	literalMinus     bool
	mailbox          string
	account          string
	validity         uint32
	conn             net.Conn
	reader           *bufio.Reader
	limits           IMAPLimits
	writer           chan struct{}
	mu               sync.Mutex
	pending          map[string]*operation
	sequence         uint64
	literal          *operation
	fetch            *operation
	flagFetch        *operation
	fetchTurns       [2]*fetchRequest
	fetchQueue       []*fetchRequest
	fetchBytes       int
	fetchPairs       uint64
	fetchWaiters     int
	closed           error
	done             chan struct{}
	changes          chan struct{}
	sequenceVersion  uint64
	sequenceChanges  [maxSequenceChanges]sequenceChange
	exists           uint32
	existsKnown      bool
	space            chan struct{}
	publicationOwner bool
	inboxOwner       bool
	bootstrapOwner   bool
	uidPlus          bool
	keywords         bool
	idleSupported    bool
	idle             *operation
	idleStop         chan struct{}
	writerWaiters    int
	timing           [commandClasses]CommandTiming
	writerWait       TimingSummary
	capacityRefusals uint64
}

// NewSelectedExecutor adopts an already authenticated, SELECTed TLS connection.
// The bootstrap owner must hand over the reader too, including buffered bytes.
// No caller may continue reading/writing the connection after this transfer.
func NewSelectedExecutor(conn net.Conn, reader *bufio.Reader, l IMAPLimits) (*Executor, error) {
	return newSelectedExecutor(conn, reader, l, "", 0, false, false, false)
}
func newSelectedExecutor(conn net.Conn, reader *bufio.Reader, l IMAPLimits, mailbox string, validity uint32, plus, minus, idle bool) (*Executor, error) {
	if conn == nil || reader == nil || l.Pending < 2 || l.Pending > 128 || l.LineBytes < 128 || l.LineBytes > 64<<10 || l.LiteralBytes < 1 || l.LiteralBytes > 2<<20 || l.ResponseBytes < l.LiteralBytes || l.ResponseBytes > 8<<20 || l.ProgressTimeout <= 0 {
		return nil, ErrCapacity
	}
	e := &Executor{writer: make(chan struct{}, 1), mailbox: mailbox, validity: validity, literalPlus: plus, literalMinus: minus, idleSupported: idle, conn: conn, reader: reader, limits: l, pending: map[string]*operation{}, done: make(chan struct{}), changes: make(chan struct{}, 1), space: make(chan struct{})}
	go e.readLoop()
	return e, nil
}
func (e *Executor) Changes() <-chan struct{} { return e.changes }

// SpaceChanged is an edge notification for pending-command capacity,
// independent of unsolicited mailbox changes and application flow activity.
func (e *Executor) SpaceChanged() <-chan struct{} { e.mu.Lock(); defer e.mu.Unlock(); return e.space }
func (e *Executor) signalSpaceLocked()            { close(e.space); e.space = make(chan struct{}) }

// SequenceVersion is only an invalidation hint. It cannot establish UID
// ownership, deletion, absence, delivery or any financial release.
func (e *Executor) SequenceVersion() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sequenceVersion
}
func (e *Executor) Close() error { e.fail(ErrClosed); return nil }
func (e *Executor) fail(err error) {
	e.mu.Lock()
	if e.closed != nil {
		e.mu.Unlock()
		return
	}
	e.closed = err
	for _, op := range e.pending {
		e.releaseFetchTurnLocked(op)
		op.result.Err = err
		e.completedLocked(op, true)
		close(op.done)
	}
	e.pending = map[string]*operation{}
	e.fetch, e.flagFetch, e.literal, e.idle = nil, nil, nil, nil
	close(e.done)
	e.signalSpaceLocked()
	e.mu.Unlock()
	e.conn.Close()
}
func (e *Executor) begin(class commandClass, fetch, literal bool) (string, *operation, error) {
	return e.beginOperation(&operation{class: class, fetch: fetch, metadata: class == commandInventory, flags: class == commandFlags}, literal)
}

func (e *Executor) beginOperation(op *operation, literal bool) (string, *operation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed != nil {
		return "", nil, e.closed
	}
	blocked := false
	if op.fetch {
		if r := op.fetchTurn; r != nil {
			if !r.granted || r.finished || r.started || r.op != op {
				return "", nil, ErrProtocol
			}
		} else {
			blocked = e.fetch != nil || e.flagFetch != nil || e.fetchTurns[0] != nil || e.fetchTurns[1] != nil
		}
	}
	if len(e.pending) >= e.limits.Pending || blocked {
		e.capacityRefusals++
		return "", nil, errCommandCapacity
	}
	e.sequence++
	tag := fmt.Sprintf("V2%016x", e.sequence)
	op.started, op.done, op.continuation = time.Now(), make(chan struct{}), make(chan struct{}, 1)
	e.timing[op.class].Started++
	e.pending[tag] = op
	if op.fetch {
		if op.flags {
			e.flagFetch = op
		} else {
			e.fetch = op
		}
		if op.fetchTurn != nil {
			op.fetchTurn.started = true
		}
		if e.fetch != nil && e.flagFetch != nil {
			e.fetchPairs++
		}
	}
	if literal {
		e.literal = op
	}
	// Independent bounded completion lifetime, not caller context. A stalled
	// provider invalidates the socket once and leaves publication outcome unknown.
	// Register while holding the completion lock: even an immediate tag or
	// Close must see and stop the timer. A callback that already fired still
	// checks op.done under that same lock before invalidating the carrier.
	// Healthy commands need no dedicated sleeping goroutine.
	op.watchdog = time.AfterFunc(e.limits.ProgressTimeout, func() { e.expire(op) })
	return tag, op, nil
}

func (e *Executor) expire(op *operation) {
	e.mu.Lock()
	select {
	case <-op.done:
		e.mu.Unlock()
		return
	default:
	}
	// Claim failure while holding the completion mutex. No tag can complete
	// between this decision and the failure state.
	if e.closed != nil {
		e.mu.Unlock()
		return
	}
	e.closed = ErrUnknown
	for _, pending := range e.pending {
		e.releaseFetchTurnLocked(pending)
		pending.result.Err = ErrUnknown
		e.completedLocked(pending, true)
		close(pending.done)
	}
	e.pending = map[string]*operation{}
	e.fetch, e.flagFetch, e.literal, e.idle = nil, nil, nil, nil
	close(e.done)
	e.signalSpaceLocked()
	e.mu.Unlock()
	e.conn.Close()
}

func (e *Executor) write(b []byte) error {
	if err := e.conn.SetWriteDeadline(time.Now().Add(e.limits.ProgressTimeout)); err != nil {
		return err
	}
	for len(b) > 0 {
		n, err := e.conn.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
		b = b[n:]
	}
	return nil
}
func quoteMailbox(s string) (string, error) {
	if s == "" || len(s) > 512 || strings.ContainsAny(s, "\r\n\x00") {
		return "", ErrProtocol
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, nil
}
func (e *Executor) Append(ctx context.Context, mailbox string, body []byte) (*Command, error) {
	if e.mailbox != "" && e.mailbox != mailbox {
		return nil, ErrProtocol
	}
	name, err := quoteMailbox(mailbox)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > e.limits.LiteralBytes {
		return nil, ErrCapacity
	}
	// Context governs admission only. Once a command line is on the wire its
	// literal barrier must be completed, even if the original app went away.
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// RFC 7888 LITERAL- permits non-synchronizing literals up to 4096
	// wire bytes. Larger bodies retain the legal synchronizing barrier.
	nonSync := e.literalPlus || e.literalMinus && len(body) <= 4096
	tag, op, err := e.begin(commandAppend, false, !nonSync)
	if err != nil {
		return nil, err
	}
	cmd := &Command{op}
	literalCount := fmt.Sprintf("{%d}", len(body))
	if nonSync {
		literalCount = fmt.Sprintf("{%d+}", len(body))
	}
	if err = e.write([]byte(fmt.Sprintf("%s APPEND %s %s\r\n", tag, name, literalCount))); err != nil {
		e.fail(ErrUnknown)
		return cmd, ErrUnknown
	}
	if !nonSync {
		select {
		case <-op.continuation:
		case <-op.done:
			return cmd, nil
		case <-e.done:
			return cmd, ErrUnknown
		}
	}
	literalBegin := time.Now()
	if err = e.write(body); err == nil {
		err = e.write([]byte("\r\n"))
	}
	e.mu.Lock()
	e.timing[commandAppend].LiteralWrite.observe(time.Since(literalBegin))
	e.mu.Unlock()
	if err != nil {
		e.fail(ErrUnknown)
		return cmd, ErrUnknown
	}
	e.mu.Lock()
	if e.literal == op {
		e.literal = nil
	}
	e.mu.Unlock()
	return cmd, nil
}
func (e *Executor) Fetch(ctx context.Context, first, last uint32) (*Command, error) {
	return e.fetchRange(ctx, first, last, false, false, false)
}

// Enumerate returns actual extant UIDs, not UIDNEXT/EXISTS allocation hints.
// Its UID-only replies consume the original per-command response budget and
// never produce their own change notifications/polling feedback loop.
func (e *Executor) Enumerate(ctx context.Context) (*Command, error) {
	return e.fetchRange(ctx, 1, ^uint32(0), true, false, false)
}
func (e *Executor) enumerateInbox(ctx context.Context, sizes bool) (*Command, error) {
	return e.fetchRange(ctx, 1, ^uint32(0), true, sizes, true)
}
func (e *Executor) fetchInbox(ctx context.Context, uid uint32) (*Command, error) {
	return e.fetchRange(ctx, uid, uid, false, false, true)
}
func (e *Executor) fetchRange(ctx context.Context, first, last uint32, metadata, sizes, queued bool) (*Command, error) {
	if first == 0 || last < first {
		return nil, ErrProtocol
	}
	class := commandBody
	if metadata {
		class = commandInventory
	}
	op := &operation{class: class, fetch: true, first: first, last: last, metadata: metadata, uids: make(map[uint32]bool)}
	if queued {
		turn, err := e.acquireFetchTurn(ctx, op)
		if err != nil {
			return nil, err
		}
		defer e.releaseUnstartedFetch(turn)
	}
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tag, op, err := e.beginOperation(op, false)
	if err != nil {
		return nil, err
	}
	items := "UID BODY.PEEK[]"
	if metadata {
		items = "UID"
		if sizes {
			items += " RFC822.SIZE"
		}
	}
	command := fmt.Sprintf("%s UID FETCH %d:%d (%s)\r\n", tag, first, last, items)
	if err = e.write([]byte(command)); err != nil {
		e.fail(ErrUnknown)
		return &Command{op}, ErrUnknown
	}
	return &Command{op}, nil
}
func (e *Executor) Noop(ctx context.Context) (*Command, error) {
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tag, op, err := e.begin(commandNoop, false, false)
	if err != nil {
		return nil, err
	}
	if err = e.write([]byte(tag + " NOOP\r\n")); err != nil {
		e.fail(ErrUnknown)
		return &Command{op}, ErrUnknown
	}
	return &Command{op}, nil
}
func (e *Executor) line() (string, error) {
	var b []byte
	for {
		part, prefix, err := e.reader.ReadLine()
		if err != nil {
			return "", err
		}
		if len(b)+len(part) > e.limits.LineBytes {
			return "", ErrCapacity
		}
		b = append(b, part...)
		if !prefix {
			return string(b), nil
		}
	}
}
func (e *Executor) readLoop() {
	for {
		line, err := e.line()
		if err != nil {
			e.fail(ErrUnknown)
			return
		}
		e.mu.Lock()
		if e.idle != nil {
			if len(line)+2 > e.limits.ResponseBytes-e.idle.bytes {
				e.mu.Unlock()
				e.fail(ErrCapacity)
				return
			}
			e.idle.bytes += len(line) + 2
		}
		e.mu.Unlock()
		if strings.HasPrefix(line, "+") {
			e.mu.Lock()
			op := e.literal
			e.literal = nil
			if op != nil && op.class == commandAppend {
				e.timing[commandAppend].Continuation.observe(time.Since(op.started))
			}
			e.mu.Unlock()
			if op == nil {
				e.fail(ErrProtocol)
				return
			}
			select {
			case op.continuation <- struct{}{}:
			default:
				e.fail(ErrProtocol)
				return
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			e.fail(ErrProtocol)
			return
		}
		if fields[0] == "*" {
			changed, noteErr := e.noteSequenceChange(fields)
			if noteErr != nil {
				e.fail(noteErr)
				return
			}
			if e.validity != 0 {
				upper := strings.ToUpper(line)
				const marker = "[UIDVALIDITY "
				if at := strings.Index(upper, marker); at >= 0 {
					tail := upper[at+len(marker):]
					end := strings.IndexByte(tail, ']')
					if end < 0 {
						e.fail(ErrProtocol)
						return
					}
					v, parseErr := strconv.ParseUint(tail[:end], 10, 32)
					if parseErr != nil || v != uint64(e.validity) {
						e.fail(ErrProtocol)
						return
					}
				}
			}
			if strings.EqualFold(fields[1], "BYE") {
				e.fail(ErrUnknown)
				return
			}
			if len(fields) >= 3 && strings.EqualFold(fields[2], "FETCH") {
				if !strings.HasSuffix(line, "}") {
					flags, err := e.readFlags(line)
					if err != nil {
						e.fail(err)
						return
					}
					if flags {
						continue
					}
					// Flags-only FETCH is a legal unsolicited update. It does
					// not complete or supply a body for our pending UID FETCH.
					if strings.Contains(strings.ToUpper(line), "BODY") {
						e.fail(ErrProtocol)
						return
					}
					metadata, err := e.readMetadata(line)
					if err != nil {
						e.fail(err)
						return
					}
					if metadata {
						continue
					}
					select {
					case e.changes <- struct{}{}:
					default:
					}
					continue
				}
				if err = e.readBody(line); err != nil {
					e.fail(err)
					return
				}
			} else if changed {
				select {
				case e.changes <- struct{}{}:
				default:
				}
			}
			continue
		}
		e.mu.Lock()
		op := e.pending[fields[0]]
		if op == nil {
			e.mu.Unlock()
			e.fail(ErrProtocol)
			return
		}
		if !strings.EqualFold(fields[1], "OK") {
			op.result.Err = ErrRefused
		}
		delete(e.pending, fields[0])
		e.signalSpaceLocked()
		if e.fetch == op {
			e.fetch = nil
		}
		if e.flagFetch == op {
			e.flagFetch = nil
		}
		e.releaseFetchTurnLocked(op)
		if e.literal == op {
			e.literal = nil
		}
		if e.idle == op {
			e.idle = nil
		}
		e.completedLocked(op, false)
		close(op.done)
		e.mu.Unlock()
	}
}
func (e *Executor) readMetadata(line string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op := e.fetch
	if op == nil || !op.metadata {
		return false, nil
	}
	fields := strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(strings.ToUpper(line)))
	var uid uint64
	var size uint32
	sizeKnown := false
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "UID" {
			value, err := strconv.ParseUint(fields[i+1], 10, 32)
			if err != nil || value == 0 || uid != 0 {
				return false, ErrProtocol
			}
			uid = value
		}
		if fields[i] == "RFC822.SIZE" {
			value, err := strconv.ParseUint(fields[i+1], 10, 32)
			if err != nil || sizeKnown {
				return false, ErrProtocol
			}
			size, sizeKnown = uint32(value), true
		}
	}
	if uid == 0 || !op.requestsUID(uint32(uid)) {
		return false, nil
	}
	if err := e.chargeFetchLocked(op, len(line)+2); err != nil {
		return false, err
	}
	if !op.uids[uint32(uid)] {
		op.uids[uint32(uid)] = true
		sequence, err := fetchSequence(line)
		if err != nil {
			return false, err
		}
		op.result.Bodies = append(op.result.Bodies, Body{UID: uint32(uid), Sequence: sequence, Size: size, SizeKnown: sizeKnown})
	}
	return true, nil
}
func (e *Executor) readBody(line string) error {
	sequence, err := fetchSequence(line)
	if err != nil {
		return err
	}
	// This executor requests one complete BODY per UID. Any partial/alternative
	// FETCH form is rejected rather than ambiguously assigned to a pending tag.
	start := strings.LastIndex(line, "{")
	if start < 0 || !strings.HasSuffix(line, "}") {
		return ErrProtocol
	}
	size, err := strconv.ParseUint(line[start+1:len(line)-1], 10, 32)
	if err != nil || size > uint64(e.limits.LiteralBytes) {
		return ErrCapacity
	}
	e.mu.Lock()
	op := e.fetch
	if op != nil && op.metadata {
		e.mu.Unlock()
		return ErrProtocol
	}
	if op == nil {
		e.mu.Unlock()
		return ErrCapacity
	}
	if err := e.chargeFetchLocked(op, int(size)+len(line)); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	body := make([]byte, int(size))
	if _, err = io.ReadFull(e.reader, body); err != nil {
		return ErrUnknown
	}
	suffix, err := e.line()
	if err != nil {
		return ErrUnknown
	}
	header := strings.ToUpper(line[:start] + " " + suffix)
	tokens := strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(header))
	var uid uint64
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "UID" {
			uid, err = strconv.ParseUint(tokens[i+1], 10, 32)
			if err != nil {
				return ErrProtocol
			}
			break
		}
	}
	if uid == 0 || !strings.Contains(header, "BODY[]") || !strings.HasSuffix(strings.TrimSpace(suffix), ")") {
		return ErrProtocol
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed != nil {
		return e.closed
	}
	if !op.requestsUID(uint32(uid)) || op.uids[uint32(uid)] {
		return ErrProtocol
	}
	if err := e.chargeFetchLocked(op, len(suffix)); err != nil {
		return err
	}
	op.uids[uint32(uid)] = true
	op.result.Bodies = append(op.result.Bodies, Body{UID: uint32(uid), Sequence: sequence, Bytes: body, sequenceVersion: e.sequenceVersion})
	return nil
}

func fetchSequence(line string) (uint32, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || fields[0] != "*" || !strings.EqualFold(fields[2], "FETCH") {
		return 0, ErrProtocol
	}
	n, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil || n == 0 {
		return 0, ErrProtocol
	}
	return uint32(n), nil
}

func (e *Executor) noteSequenceChange(fields []string) (bool, error) {
	if len(fields) != 3 || !strings.EqualFold(fields[2], "EXISTS") && !strings.EqualFold(fields[2], "EXPUNGE") {
		return false, nil
	}
	n, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil || strings.EqualFold(fields[2], "EXPUNGE") && n == 0 {
		return false, ErrProtocol
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	invalidate := strings.EqualFold(fields[2], "EXPUNGE")
	changed := invalidate || !e.existsKnown || e.exists != uint32(n)
	if invalidate {
		if e.existsKnown && e.exists > 0 {
			e.exists--
		}
	} else {
		invalidate = e.existsKnown && uint32(n) < e.exists
		e.exists, e.existsKnown = uint32(n), true
	}
	if invalidate {
		if e.sequenceVersion == ^uint64(0) {
			return false, ErrProtocol
		}
		e.sequenceVersion++
		event := sequenceChange{version: e.sequenceVersion}
		if strings.EqualFold(fields[2], "EXPUNGE") {
			event.expunged = uint32(n)
		}
		e.sequenceChanges[e.sequenceVersion%maxSequenceChanges] = event
	}
	return changed, nil
}

// Queued, unstarted commands obey application cancellation without waiting for
// another operation's literal barrier. Started command draining is separate.
func (e *Executor) lockWriter(ctx context.Context) error {
	begin := time.Now()
	defer func() {
		e.mu.Lock()
		e.writerWait.observe(time.Since(begin))
		e.mu.Unlock()
	}()
	e.mu.Lock()
	if e.closed != nil {
		err := e.closed
		e.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	e.writerWaiters++
	// Request DONE even if the IDLE continuation has not arrived yet. The
	// IDLE owner drains that continuation and its tag before releasing bytes.
	if e.idleStop != nil {
		close(e.idleStop)
		e.idleStop = nil
	}
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.writerWaiters--; e.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return ErrClosed
	case e.writer <- struct{}{}:
		return nil
	}
}
