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

type Body struct {
	UID   uint32
	Bytes []byte
}
type Result struct {
	Bodies []Body
	Err    error
}
type operation struct {
	done         chan struct{}
	continuation chan struct{}
	result       Result
	bytes        int
	fetch        bool
	first, last  uint32
	uids         map[uint32]bool
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
// and one UID FETCH may overlap. UID FETCH operations serialize because their
// untagged responses have no command tag. SELECT/EXPUNGE/IDLE are deliberately
// not issued concurrently; mailbox changes require a new executor.
type Executor struct {
	literalPlus  bool
	literalMinus bool
	mailbox      string
	validity     uint32
	conn         net.Conn
	reader       *bufio.Reader
	limits       IMAPLimits
	writer       chan struct{}
	mu           sync.Mutex
	pending      map[string]*operation
	sequence     uint64
	literal      *operation
	fetch        *operation
	closed       error
	done         chan struct{}
	changes      chan struct{}
}

// NewSelectedExecutor adopts an already authenticated, SELECTed TLS connection.
// The bootstrap owner must hand over the reader too, including buffered bytes.
// No caller may continue reading/writing the connection after this transfer.
func NewSelectedExecutor(conn net.Conn, reader *bufio.Reader, l IMAPLimits) (*Executor, error) {
	return newSelectedExecutor(conn, reader, l, "", 0, false, false)
}
func newSelectedExecutor(conn net.Conn, reader *bufio.Reader, l IMAPLimits, mailbox string, validity uint32, plus, minus bool) (*Executor, error) {
	if conn == nil || reader == nil || l.Pending < 2 || l.Pending > 128 || l.LineBytes < 128 || l.LineBytes > 64<<10 || l.LiteralBytes < 1 || l.LiteralBytes > 2<<20 || l.ResponseBytes < l.LiteralBytes || l.ResponseBytes > 8<<20 || l.ProgressTimeout <= 0 {
		return nil, ErrCapacity
	}
	e := &Executor{writer: make(chan struct{}, 1), mailbox: mailbox, validity: validity, literalPlus: plus, literalMinus: minus, conn: conn, reader: reader, limits: l, pending: map[string]*operation{}, done: make(chan struct{}), changes: make(chan struct{}, 1)}
	go e.readLoop()
	return e, nil
}
func (e *Executor) Changes() <-chan struct{} { return e.changes }
func (e *Executor) Close() error             { e.fail(ErrClosed); return nil }
func (e *Executor) fail(err error) {
	e.mu.Lock()
	if e.closed != nil {
		e.mu.Unlock()
		return
	}
	e.closed = err
	for _, op := range e.pending {
		op.result.Err = err
		close(op.done)
	}
	e.pending = map[string]*operation{}
	close(e.done)
	e.mu.Unlock()
	e.conn.Close()
}
func (e *Executor) begin(fetch, literal bool) (string, *operation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed != nil {
		return "", nil, e.closed
	}
	if len(e.pending) >= e.limits.Pending || fetch && e.fetch != nil {
		return "", nil, ErrCapacity
	}
	e.sequence++
	tag := fmt.Sprintf("V2%016x", e.sequence)
	op := &operation{done: make(chan struct{}), continuation: make(chan struct{}, 1), fetch: fetch}
	e.pending[tag] = op
	if fetch {
		e.fetch = op
	}
	if literal {
		e.literal = op
	}
	// Independent bounded completion lifetime, not caller context. A stalled
	// provider invalidates the socket once and leaves publication outcome unknown.
	go func() {
		timer := time.NewTimer(e.limits.ProgressTimeout)
		defer timer.Stop()
		select {
		case <-op.done:
		case <-timer.C:
			e.expire(op)
		}
	}()
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
		pending.result.Err = ErrUnknown
		close(pending.done)
	}
	e.pending = map[string]*operation{}
	close(e.done)
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
	tag, op, err := e.begin(false, !nonSync)
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
	if err = e.write(body); err == nil {
		err = e.write([]byte("\r\n"))
	}
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
	if first == 0 || last < first {
		return nil, ErrProtocol
	}
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tag, op, err := e.begin(true, false)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	op.first = first
	op.last = last
	op.uids = make(map[uint32]bool)
	e.mu.Unlock()
	if err = e.write([]byte(fmt.Sprintf("%s UID FETCH %d:%d (UID BODY.PEEK[])\r\n", tag, first, last))); err != nil {
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
	tag, op, err := e.begin(false, false)
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
		if strings.HasPrefix(line, "+") {
			e.mu.Lock()
			op := e.literal
			e.literal = nil
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
					// Flags-only FETCH is a legal unsolicited update. It does
					// not complete or supply a body for our pending UID FETCH.
					if strings.Contains(strings.ToUpper(line), "BODY") {
						e.fail(ErrProtocol)
						return
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
			} else {
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
		if e.fetch == op {
			e.fetch = nil
		}
		if e.literal == op {
			e.literal = nil
		}
		close(op.done)
		e.mu.Unlock()
	}
}
func (e *Executor) readBody(line string) error {
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
	if op == nil || int(size)+len(line) > e.limits.ResponseBytes-op.bytes {
		e.mu.Unlock()
		return ErrCapacity
	}
	op.bytes += int(size) + len(line)
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
	if uint32(uid) < op.first || uint32(uid) > op.last || op.uids[uint32(uid)] {
		return ErrProtocol
	}
	if len(suffix) > e.limits.ResponseBytes-op.bytes {
		return ErrCapacity
	}
	op.bytes += len(suffix)
	op.uids[uint32(uid)] = true
	op.result.Bodies = append(op.result.Bodies, Body{UID: uint32(uid), Bytes: body})
	return nil
}

// Queued, unstarted commands obey application cancellation without waiting for
// another operation's literal barrier. Started command draining is separate.
func (e *Executor) lockWriter(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return ErrClosed
	case e.writer <- struct{}{}:
		return nil
	}
}
