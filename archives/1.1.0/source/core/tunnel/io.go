package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
)

const (
	accountDataWorkers    = 2
	accountQueuePerUser   = 4
	accountQueuedMIMEHard = 4 << 20
)

type appendClient interface {
	Append(context.Context, string, []byte) error
	Close() error
}

// accountRuntime owns bounded physical DATA APPEND capacity for one provider
// account on the VPS. Both users share the same workers, but admission is kept
// in per-user queues and workers pick them round-robin so one bulk user cannot
// permanently sit in front of the other. Control APPENDs do not enter this
// runtime and therefore cannot be head-of-line blocked by DATA.
type accountRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc
	dial   func(context.Context) (appendClient, error)
	gate   *accountDialGate

	fastQueues map[string][]accountAppend
	fastStreak int

	mu            sync.Mutex
	cond          *sync.Cond
	users         []string
	queues        map[string][]accountAppend
	outstanding   map[string]int
	deficit       map[string]int
	workerSockets map[int]appendClient
	seeds         []appendClient
	workerRepair  map[int]bool
	next          int
	pendingBytes  int
	appendHints   []runtimeAppendHint
	busy          int
	completed     int64
	failed        int64
	servedBytes   int64
	maxQueueWait  time.Duration
	maxService    time.Duration
	userDone      map[string]int64
	userBytes     map[string]int64
	capacityWake  map[*laneMail]func()
	live          atomic.Int32
	workers       sync.WaitGroup
	done          chan struct{}
}

type runtimeAppendHint struct {
	user string
	mailbox.AppendHint
}

type accountAppend struct {
	fast     bool
	ctx      context.Context
	user     string
	folder   string
	raw      []byte
	done     chan error
	queuedAt time.Time
}

func newAccountRuntime(parent context.Context, users []string, dial func(context.Context) (appendClient, error)) *accountRuntime {
	return newAccountRuntimeWorkers(parent, users, dial, accountDataWorkers)
}
func newAccountRuntimeWorkers(parent context.Context, users []string, dial func(context.Context) (appendClient, error), workers int) *accountRuntime {
	return newAccountRuntimeSeeded(parent, users, dial, workers, nil)
}
func newAccountRuntimeSeeded(parent context.Context, users []string, dial func(context.Context) (appendClient, error), workers int, seeds []appendClient) *accountRuntime {
	ctx, cancel := context.WithCancel(parent)
	r := &accountRuntime{ctx: ctx, cancel: cancel, gate: newAccountDialGate(), queues: map[string][]accountAppend{}, fastQueues: map[string][]accountAppend{}, outstanding: map[string]int{}, deficit: map[string]int{}, workerSockets: map[int]appendClient{}, workerRepair: map[int]bool{}, userDone: map[string]int64{}, userBytes: map[string]int64{}, done: make(chan struct{})}
	r.seeds = seeds
	r.dial = func(ctx context.Context) (writer appendClient, err error) {
		err = r.gate.run(ctx, func() error { writer, err = dial(ctx); return err })
		return writer, err
	}
	for _, user := range users {
		if user == "" {
			continue
		}
		if _, exists := r.queues[user]; exists {
			continue
		}
		r.users = append(r.users, user)
		r.queues[user] = nil
		r.outstanding[user] = 0
	}
	r.cond = sync.NewCond(&r.mu)
	// Context cancellation changes the wait predicate outside r.mu. Acquire
	// the same lock before broadcasting, so it cannot race a check->Wait gap.
	context.AfterFunc(ctx, r.wakeWaiters)
	r.live.Store(int32(workers))
	for i := 0; i < workers; i++ {
		r.workers.Add(1)
		go r.runWorker(i)
	}
	go func() {
		r.workers.Wait()
		r.failQueued(r.ctx.Err())
		close(r.done)
	}()
	return r
}

func (r *accountRuntime) take() (accountAppend, bool) { return r.takeWorker(-1) }

func (r *accountRuntime) takeWorker(workerID int) (accountAppend, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if r.ctx.Err() != nil {
			return accountAppend{}, false
		}
		if r.workerRepair[workerID] {
			return accountAppend{}, true
		}
		hasFast, hasBulk := false, false
		for _, user := range r.users {
			hasFast = hasFast || len(r.fastQueues[user]) > 0
			hasBulk = hasBulk || len(r.queues[user]) > 0
		}
		fast := hasFast && (r.fastStreak < 4 || !hasBulk)
		queues := r.queues
		if fast {
			queues = r.fastQueues
		}
		queued := false
		for offset := 0; offset < len(r.users); offset++ {
			index := (r.next + offset) % len(r.users)
			user := r.users[index]
			q := queues[user]
			key := user
			if fast {
				key = "fast/" + user
			}
			if len(q) == 0 {
				r.deficit[key] = 0
				continue
			}
			queued = true
			req := q[0]
			if len(req.raw) > r.deficit[key] {
				r.deficit[key] += accountByteQuantum
				if len(req.raw) > r.deficit[key] {
					continue
				}
			}
			r.deficit[key] -= len(req.raw)
			copy(q, q[1:])
			q[len(q)-1] = accountAppend{}
			queues[user] = q[:len(q)-1]
			r.next = (index + 1) % len(r.users)
			if len(queues[user]) > 0 && len(queues[user][0].raw) <= r.deficit[key] {
				r.next = index
			}
			if fast {
				r.fastStreak++
			} else {
				r.fastStreak = 0
			}
			r.busy++
			if !req.queuedAt.IsZero() {
				wait := time.Since(req.queuedAt)
				if wait > r.maxQueueWait {
					r.maxQueueWait = wait
				}
			}
			// Wake blocked admission as soon as a queue slot becomes available.
			// The byte/outstanding budget is intentionally retained until this
			// physical request finishes, so in-flight APPENDs are bounded too.
			r.cond.Broadcast()
			return req, true
		}
		// Accumulate another bounded quantum for large messages. Sleeping here
		// would deadlock a non-empty queue with no new admission notification.
		if queued {
			r.next = (r.next + 1) % len(r.users)
			continue
		}
		r.cond.Wait()
	}
}

func (r *accountRuntime) requeueFront(req accountAppend) {
	r.mu.Lock()
	if r.busy > 0 {
		r.busy--
	}
	req.queuedAt = time.Now()
	queues := r.queues
	if req.fast {
		queues = r.fastQueues
	}
	q := queues[req.user]
	q = append(q, accountAppend{})
	copy(q[1:], q[:len(q)-1])
	q[0] = req
	queues[req.user] = q
	r.mu.Unlock()
	r.cond.Signal()
}

func (r *accountRuntime) release(req accountAppend, err error, service time.Duration) {
	r.mu.Lock()
	if r.busy > 0 {
		r.busy--
	}
	if r.outstanding[req.user] > 0 {
		r.outstanding[req.user]--
	}
	if service > r.maxService {
		r.maxService = service
	}
	if err == nil {
		r.completed++
		r.servedBytes += int64(len(req.raw))
		r.userDone[req.user]++
		r.userBytes[req.user] += int64(len(req.raw))
	} else {
		r.failed++
	}
	r.pendingBytes -= len(req.raw)
	if r.pendingBytes < 0 {
		// This is defensive only; keeping a hard lower bound here prevents a
		// bookkeeping bug from accidentally opening unlimited admission.
		r.pendingBytes = 0
	}
	r.mu.Unlock()
	r.cond.Broadcast()
}

func (r *accountRuntime) suspendWorker(req accountAppend, err error) bool {
	if !connectionLimitFallback(err) {
		return false
	}
	for {
		live := r.live.Load()
		if live <= 1 {
			return false
		}
		if r.live.CompareAndSwap(live, live-1) {
			r.requeueFront(req)
			r.notifyCapacity()
			return true
		}
	}
}

// A connection cap only suspends optional capacity. The request was never
// APPENDed and remains owned by the bounded queue; the surviving worker serves
// it. Probe with the runtime lifetime, not that already-requeued request's ctx.
func (r *accountRuntime) recoverWorker() appendClient {
	writer, err := r.waitDataWriter(r.ctx, true)
	if err != nil {
		return nil
	}
	r.live.Add(1)
	r.notifyCapacity()
	return writer
}

func (r *accountRuntime) waitDataWriter(ctx context.Context, optional bool) (appendClient, error) {
	delay := time.Second
	for {
		if err := dataRepairPause(ctx, r.gate.repairDelay(delay)); err != nil {
			return nil, err
		}
		writer, err := r.dial(ctx)
		if err == nil {
			if ctx.Err() != nil {
				_ = writer.Close()
				return nil, ctx.Err()
			}
			return writer, nil
		}
		if !optional && !connectionLimitFallback(err) && !transientIMAPDialReason(mailbox.FailureReason(err)) {
			// Never retry credentials, TLS/certificate verification, permissions,
			// or permanent DNS errors. Transient dial failures have not begun an
			// APPEND: keep the bounded, journal-owned request and reconnect with
			// backoff instead of making the entire lane reconcile a fake unknown.
			return nil, err
		}
		delay = nextDataRepairDelay(delay)
	}
}

func nextDataRepairDelay(delay time.Duration) time.Duration {
	return min(30*time.Second, 2*delay)
}

// Desynchronize account retries without permitting an unbounded dial storm.
func dataRepairPause(ctx context.Context, delay time.Duration) error {
	return pause(ctx, dataRepairDelay(delay))
}

// The backoff is a ceiling, including jitter. The former +/-20% multiplier
// stretched the nominal 1+2+4 second recovery budget to 8.4 seconds and the
// nominal 30 second cap to 36 seconds. A bounded subtractive jitter keeps
// independent workers desynchronized while preserving the stated deadlines.
func dataRepairDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	return delay - time.Duration(rand.Int64N(int64(delay/5)+1))
}

func (r *accountRuntime) setCapacityWake(owner *laneMail, wake func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if wake == nil {
		delete(r.capacityWake, owner)
		return
	}
	if r.capacityWake == nil {
		r.capacityWake = make(map[*laneMail]func())
	}
	r.capacityWake[owner] = wake
}

func (r *accountRuntime) notifyCapacity() {
	r.mu.Lock()
	wakes := make([]func(), 0, len(r.capacityWake))
	for _, wake := range r.capacityWake {
		wakes = append(wakes, wake)
	}
	r.mu.Unlock()
	for _, wake := range wakes {
		wake()
	}
}

func (r *accountRuntime) operationContext(req context.Context) (context.Context, context.CancelFunc) {
	// Admission observes the caller context, while an admitted physical APPEND
	// is also owned by the account runtime. Linking both lifetimes is required:
	// lane cancellation may stop a request, and runtime shutdown must always be
	// able to interrupt an in-flight IMAP command even if the caller is still
	// alive. mailbox.Client conservatively maps cancellation after APPEND starts
	// to ErrUnknown, so this does not create a false refund.
	ctx, cancel := context.WithCancel(r.ctx)
	stop := context.AfterFunc(req, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (r *accountRuntime) runWorker(workerID int) {
	defer r.workers.Done()
	var writer appendClient
	if workerID < len(r.seeds) {
		writer = r.seeds[workerID]
		if writer != nil {
			r.publishWorker(workerID, writer)
		}
	}
	defer func() {
		r.mu.Lock()
		delete(r.workerSockets, workerID)
		r.mu.Unlock()
		if writer != nil {
			_ = writer.Close()
		}
	}()
	for {
		req, ok := r.takeWorker(workerID)
		if !ok {
			return
		}
		// An idle socket closure wakes this worker independently of its queue.
		// No journal job is created and no previous APPEND is replayed.
		if req.done == nil {
			if writer != nil {
				_ = writer.Close()
				writer = nil
			}
			fresh, err := r.waitDataWriter(r.ctx, true)
			if err != nil {
				return
			}
			writer = fresh
			r.publishWorker(workerID, writer)
			continue
		}
		workStarted := time.Now()
		if req.ctx.Err() != nil {
			err := req.ctx.Err()
			clear(req.raw)
			r.release(req, err, time.Since(workStarted))
			req.done <- err
			continue
		}
		opCtx, stopOperation := r.operationContext(req.ctx)
	prepareWriter:
		// An observed decoder EOF is a dead idle worker, not a publication.
		// Redial before APPEND. A failure racing the actual write remains unknown.
		if ready, ok := writer.(interface{ Ready() bool }); ok && !ready.Ready() {
			_ = writer.Close()
			writer = nil
		}
		if writer == nil {
			var err error
			writer, err = r.dial(opCtx)
			if err != nil {
				// A failed dial may return a typed nil *mailbox.Client inside
				// appendClient. It owns no socket and must not survive into the
				// next APPEND or the worker's deferred Close.
				writer = nil
				if r.suspendWorker(req, err) {
					stopOperation()
					writer = r.recoverWorker()
					if writer == nil {
						return
					}
					continue
				}
				if connectionLimitFallback(err) || transientIMAPDialReason(mailbox.FailureReason(err)) {
					// A dropped network, refused TCP dial or provider connection cap
					// has not started APPEND. Keep the same bounded physical job until
					// a paced redial succeeds. Never invent an Unknown publication
					// merely because connectivity temporarily disappeared.
					writer, err = r.waitDataWriter(opCtx, false)
				}
				if err != nil {
					stopOperation()
					clear(req.raw)
					r.release(req, err, time.Since(workStarted))
					req.done <- err
					continue
				}
			}
		}
		r.publishWorker(workerID, writer)
		release, err := r.gate.work.acquire(opCtx, workData)
		if err == nil {
			err = writer.Append(opCtx, req.folder, req.raw)
			release()
		}
		if errors.Is(err, mailbox.ErrNotStarted) {
			_ = writer.Close()
			writer = nil
			goto prepareWriter
		}
		if err == nil {
			if source, ok := writer.(interface {
				AppendHints(string, map[[32]byte]bool) []mailbox.AppendHint
			}); ok {
				if body, decodeErr := mailbox.Decode(req.raw); decodeErr == nil {
					for _, hint := range source.AppendHints(req.folder, map[[32]byte]bool{sha256.Sum256(body): true}) {
						r.mu.Lock()
						if len(r.appendHints) == 256 {
							copy(r.appendHints, r.appendHints[1:])
							r.appendHints = r.appendHints[:255]
						}
						r.appendHints = append(r.appendHints, runtimeAppendHint{req.user, hint})
						r.mu.Unlock()
					}
				}
			}
		}
		stopOperation()
		clear(req.raw)
		if err != nil {
			_ = writer.Close()
			writer = nil
			r.mu.Lock()
			delete(r.workerSockets, workerID)
			r.mu.Unlock()
		}
		r.release(req, err, time.Since(workStarted))
		req.done <- err
	}
}

func (r *accountRuntime) failQueued(err error) {
	if err == nil {
		err = context.Canceled
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, user := range r.users {
		for _, req := range append(r.queues[user], r.fastQueues[user]...) {
			clear(req.raw)
			select {
			case req.done <- err:
			default:
			}
		}
		r.queues[user] = nil
		r.fastQueues[user] = nil
		r.outstanding[user] = 0
	}
	r.pendingBytes = 0
	r.cond.Broadcast()
}

func (r *accountRuntime) Append(ctx context.Context, user, folder string, raw []byte) error {
	return r.AppendClass(ctx, user, folder, raw, false)
}
func (r *accountRuntime) AppendClass(ctx context.Context, user, folder string, raw []byte, fast bool) error {
	if r == nil {
		return errors.New("ACCOUNT_RUNTIME_MISSING")
	}
	if len(raw) == 0 || len(raw) > mailbox.MaxMIME {
		return mailbox.ErrLimit
	}
	r.mu.Lock()
	_, known := r.queues[user]
	if !known {
		r.mu.Unlock()
		return mailbox.ErrBusy
	}
	// Do not reject a physically valid DATA batch just because another APPEND
	// is in flight. Rejection here happens after the durable journal Start and
	// would turn ordinary load into CommitUnknown/reconnect churn. Instead, use
	// bounded backpressure: at most four outstanding requests per user and
	// accountQueuedMIMEHard bytes across queued + in-flight APPENDs.
	for (r.outstanding[user] >= accountQueuePerUser || r.pendingBytes+len(raw) > accountQueuedMIMEHard) && r.ctx.Err() == nil && ctx.Err() == nil {
		// sync.Cond has no context-aware wait. A short bounded wake keeps
		// pre-admission cancellation responsive without spawning an unbounded
		// helper goroutine per queued batch. Once admitted, cancellation is
		// intentionally not used to infer a physical APPEND outcome.
		timer := time.AfterFunc(50*time.Millisecond, r.wakeWaiters)
		r.cond.Wait()
		_ = timer.Stop()
	}
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return err
	}
	if r.ctx.Err() != nil {
		r.mu.Unlock()
		return r.ctx.Err()
	}
	req := accountAppend{fast: fast, ctx: ctx, user: user, folder: folder, raw: bytes.Clone(raw), done: make(chan error, 1), queuedAt: time.Now()}
	if fast {
		r.fastQueues[user] = append(r.fastQueues[user], req)
	} else {
		r.queues[user] = append(r.queues[user], req)
	}
	r.outstanding[user]++
	r.pendingBytes += len(req.raw)
	r.mu.Unlock()
	r.cond.Signal()

	// Once admitted, the runtime owns the cloned body and physical outcome.
	// Waiting through caller cancellation is intentional: returning early could
	// let the journal infer Unknown while APPEND is still physically executing.
	select {
	case err := <-req.done:
		return err
	case <-r.done:
		select {
		case err := <-req.done:
			return err
		default:
			return r.ctx.Err()
		}
	}
}

type accountRuntimeSnapshot struct {
	LiveWorkers    int
	BusyWorkers    int
	QueuedRequests int
	PendingBytes   int
	Completed      int64
	Failed         int64
	ServedBytes    int64
	MaxQueueWaitMS int64
	MaxServiceMS   int64
	UserCompleted  map[string]int64
	UserBytes      map[string]int64
}

func (r *accountRuntime) snapshot() accountRuntimeSnapshot {
	if r == nil {
		return accountRuntimeSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := accountRuntimeSnapshot{
		LiveWorkers:    max(0, int(r.live.Load())-len(r.workerRepair)),
		BusyWorkers:    r.busy,
		PendingBytes:   r.pendingBytes,
		Completed:      r.completed,
		Failed:         r.failed,
		ServedBytes:    r.servedBytes,
		MaxQueueWaitMS: r.maxQueueWait.Milliseconds(),
		MaxServiceMS:   r.maxService.Milliseconds(),
		UserCompleted:  make(map[string]int64, len(r.userDone)),
		UserBytes:      make(map[string]int64, len(r.userBytes)),
	}
	for user, q := range r.queues {
		s.QueuedRequests += len(q) + len(r.fastQueues[user])
		s.UserCompleted[user] = r.userDone[user]
		s.UserBytes[user] = r.userBytes[user]
	}
	return s
}

func (r *accountRuntime) Parallelism() int {
	if r == nil {
		return 1
	}
	n := r.availableWorkers()
	if n < 1 {
		return 1
	}
	return n
}

func (r *accountRuntime) wakeWaiters() {
	r.mu.Lock()
	r.cond.Broadcast()
	r.mu.Unlock()
}

func (r *accountRuntime) Close() {
	if r == nil {
		return
	}
	r.cancel()
	r.wakeWaiters()
	<-r.done
}

// The handshake/base connection becomes the latency-sensitive control path after
// Activate. Reader, DATA and (when the provider allows it) maintenance/GC use
// independent sockets so slow SELECT/EXPUNGE or APPEND work cannot block OPEN,
// WINDOW, RECEIPT or liveness traffic. A second DATA writer is best-effort;
// providers with a lower connection limit safely fall back to one writer.
type laneMail struct {
	baseWork       accountWorkBudget // one fair command turn on the shared control socket
	dataWork       accountWorkBudget
	dataChecks     map[string]time.Time
	dataReadLimit  uint32 // command mutex; large-BODY evidence only, reset on replacement
	dataDiscovery  map[string]mailbox.TailDiscovery
	topologyMu     sync.Mutex
	topology       servercfg.Transport
	dataReader     *mailbox.Client
	dataReaderWake chan struct{}
	borrowHintMu   sync.Mutex
	borrowHints    []mailbox.AppendHint
	topologyCaps   uint64
	writerTarget   int

	readerStateMu      sync.Mutex
	readerCommandMu    sync.Mutex
	readerPages        receivePagePolicy
	readerRepairs      atomic.Uint64
	providerRTT        atomic.Int64
	previousReader     atomic.Pointer[mailbox.SocketSnapshot]
	previousDataReader atomic.Pointer[mailbox.SocketSnapshot]
	*mailbox.Client
	controlGeneration       atomic.Pointer[mailbox.Client]
	controlRepairMu         sync.Mutex
	previousControl         atomic.Pointer[mailbox.SocketSnapshot]
	dial                    func(context.Context) (*mailbox.Client, error)
	reader                  *mailbox.Client
	cleaner                 *mailbox.Client
	cleanerMu               sync.Mutex
	cleanerRepairing        bool
	readerCheck             time.Time
	handshakeRecv           string
	readerHotUntil          atomic.Int64
	readerHotRequestAt      atomic.Int64 // diagnostic-only timestamp of latest latency-critical reader wake
	writerMu                sync.Mutex
	writers                 []*mailbox.Client
	writerDead              []bool
	writerBusy              []bool
	writerTurn              atomic.Uint32
	dataMaintenanceIsolated atomic.Bool
	baseData                bool
	repairing               bool
	repairWG                sync.WaitGroup
	dataCtx                 context.Context
	dataCancel              context.CancelFunc
	dataWake                func()
	runtime                 *accountRuntime
	runtimeUser             string
	recv                    string
	once                    sync.Once
	modeError               string
	closed                  bool // writerMu; forbids late repair/wake registration.
	dialGate                *accountDialGate
	stageSecondDataWriter   bool // Client startup: defer only the second optional DATA socket.
}

func (m *laneMail) Activate(ctx context.Context, recv string) error {
	if m.dialGate == nil {
		m.dialGate = newAccountDialGate()
		if m.runtime != nil {
			m.dialGate = m.runtime.gate
		}
		dial := m.dial
		m.dial = func(ctx context.Context) (client *mailbox.Client, err error) {
			err = m.dialGate.run(ctx, func() error { client, err = dial(ctx); return err })
			return client, err
		}
	}
	repairCtx, repairCancel := context.WithCancel(ctx)
	m.writerMu.Lock()
	m.dataCtx = repairCtx
	m.dataCancel = repairCancel
	m.writerMu.Unlock()
	// These sockets improve throughput; the authenticated base socket can
	// carry activation and DATA by itself. A stalled extra LOGIN must not hold
	// CONFIRM behind the provider's 30-second connection deadline. Keep repair
	// on the parent lifetime, not this short-lived setup budget.
	optionalCtx, stopOptional := context.WithTimeout(ctx, 4*time.Second)
	defer stopOptional()
	optionalFailure := func(err error) bool {
		return connectionLimitFallback(err) || (optionalCtx.Err() != nil && ctx.Err() == nil)
	}
	cleaner := func() error {
		err := m.activateCleaner(optionalCtx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if optionalCtx.Err() != nil {
			return nil
		}
		return err
	}
	var err error
	var firstWriter *mailbox.Client
	var firstDialErr error
	if m.stageSecondDataWriter && m.runtime == nil && m.dataWriterTarget() > 1 {
		// Exactly two client logins may overlap. Collect both results before
		// publishing sockets; a failure cannot leak the successful sibling.
		type dialResult struct {
			client *mailbox.Client
			err    error
		}
		first := make(chan dialResult, 1)
		go func() { c, e := m.dial(optionalCtx); first <- dialResult{c, e} }()
		m.reader, err = m.dial(optionalCtx)
		result := <-first
		firstWriter, firstDialErr = result.client, result.err
		if err != nil && (ctx.Err() != nil || !optionalFailure(err)) && firstWriter != nil {
			firstWriter.Close()
			firstWriter = nil
		}
	} else {
		m.reader, err = m.dial(optionalCtx)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if optionalFailure(err) {
			// The reader is optional. Do not return early here: doing so leaves
			// the lane authenticated but with neither DATA writers nor the
			// intentional base-connection fallback. Continue through DATA
			// activation so a provider connection cap degrades to serialized
			// base I/O instead of a permanently DATA-unavailable carrier.
			m.modeError = errorCode(err)
			m.reader = nil
		} else {
			return fmt.Errorf("receive_connect: %w", err)
		}
	}
	m.recv = recv
	if m.runtime != nil {
		m.dataMaintenanceIsolated.Store(true)
		return cleaner()
	}
	for i := 0; i < m.dataWriterTarget(); i++ {
		if m.stageSecondDataWriter && i > 0 {
			// The first DATA connection and the isolated maintenance socket
			// are already authenticated. Do not hold initial line readiness
			// behind the optional second DATA login. The existing bounded,
			// paced repair machinery brings it online without touching base,
			// reader or the working first writer. Never publish fictitious
			// parallelism: the placeholder remains dead until real dial succeeds.
			m.writerMu.Lock()
			m.writers = append(m.writers, nil)
			m.writerDead = append(m.writerDead, true)
			m.writerBusy = append(m.writerBusy, false)
			m.writerMu.Unlock()
			break
		}
		var writer *mailbox.Client
		var dialErr error
		if i == 0 && m.stageSecondDataWriter && m.dataWriterTarget() > 1 {
			writer, dialErr = firstWriter, firstDialErr
		} else {
			writer, dialErr = m.dial(optionalCtx)
		}
		if dialErr == nil {
			m.writerMu.Lock()
			m.writers = append(m.writers, writer)
			m.writerDead = append(m.writerDead, false)
			m.writerBusy = append(m.writerBusy, false)
			m.writerMu.Unlock()
			if i == 0 {
				// Under a provider connection cap, protect the control socket from
				// GC before asking for the second optional DATA writer. Four striped
				// accounts with one writer each still provide ample physical parallelism,
				// while serialized EXPUNGE on control directly damages interactive RTT.
				if err := cleaner(); err != nil {
					return err
				}
			}
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Parallel DATA capacity is optional after one good writer. Connection
		// limit errors also fall back to the authenticated base socket if none
		// could be opened, without weakening TLS or delivery semantics.
		m.writerMu.Lock()
		haveWriter := len(m.writers) > 0
		m.writerMu.Unlock()
		if haveWriter || optionalFailure(dialErr) {
			m.modeError = errorCode(dialErr)
			if !haveWriter {
				// Provider refused optional DATA sockets. The authenticated base
				// connection remains the intentional single-writer fallback.
				m.baseData = true
			}
			// Keep refused optional positions, including a first-writer fallback,
			// so paced repair can restore capacity after the provider cap clears.
			m.writerMu.Lock()
			for len(m.writers) < m.dataWriterTarget() {
				m.writers = append(m.writers, nil)
				m.writerDead = append(m.writerDead, true)
				m.writerBusy = append(m.writerBusy, false)
			}
			m.writerMu.Unlock()
			break
		}
		if reader := m.readerRef(); reader != nil {
			_ = reader.Close()
		}
		return fmt.Errorf("append_connect: %w", dialErr)
	}
	if err := cleaner(); err != nil {
		return err
	}
	m.writerMu.Lock()
	startRepair := m.startDataRepairLocked()
	m.dataMaintenanceIsolated.Store(!m.baseData)
	m.writerMu.Unlock()
	if startRepair {
		go m.repairDataWriters()
	}
	m.writerMu.Lock()
	writers := append([]*mailbox.Client(nil), m.writers...)
	m.writerMu.Unlock()
	for i, writer := range writers {
		if writer != nil {
			m.watchDataWriter(i, writer)
		}
	}
	return nil
}

// DataMaintenanceIsolated is fixed before carrier publication. Physical worker
// repair never migrates these DATA sockets onto the maintenance/base role.
func (m *laneMail) DataMaintenanceIsolated() bool { return m.dataMaintenanceIsolated.Load() }

// activateCleaner isolates mailbox GC/inventory from the latency-sensitive base
// control connection. It is deliberately best-effort: providers with a strict
// connection cap keep the previous serialized fallback instead of losing an
// otherwise healthy authenticated lane. The first DATA writer is opened before it,
// but this maintenance socket is preferred over a second DATA writer under a cap.
func (m *laneMail) activateCleaner(ctx context.Context) error {
	m.cleanerMu.Lock()
	defer m.cleanerMu.Unlock()
	if m.dial == nil || m.cleaner != nil {
		return nil
	}
	cleaner, err := m.dial(ctx)
	if err == nil {
		m.cleaner = cleaner
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Cleanup can safely remain on the authenticated base connection when the
	// provider refuses one more optional connection. Never weaken TLS or retry
	// aggressively just to obtain the isolation optimization.
	return nil
}

// Append remains the conservative generic path used during handshake/recovery.
func (m *laneMail) Append(ctx context.Context, f string, raw []byte) error {
	return m.controlCommand(ctx, workUrgent, func() error { return m.controlClient().Append(ctx, f, raw) })
}
func (m *laneMail) AppendControl(ctx context.Context, f string, raw []byte) error {
	return m.controlCommand(ctx, workUrgent, func() error { return m.controlClient().Append(ctx, f, raw) })
}
func (m *laneMail) reserveDataWriter() (index int, writer *mailbox.Client, reserved, base bool) {
	m.writerMu.Lock()
	defer m.writerMu.Unlock()
	n := len(m.writers)
	start := 0
	if n > 0 {
		start = int(m.writerTurn.Add(1)-1) % n
	}
	// Prefer an actually idle authenticated connection. physicalSlots bounds the
	// number of concurrent callers to DataParallelism, but APPEND durations need
	// not finish in round-robin order. Blindly choosing the next index can queue
	// behind a slow writer while another connection is already idle.
	for off := 0; off < n; off++ {
		i := (start + off) % n
		if i >= len(m.writerDead) || m.writerDead[i] || m.writers[i] == nil {
			continue
		}
		if i < len(m.writerBusy) && !m.writerBusy[i] {
			m.writerBusy[i] = true
			return i, m.writers[i], true, m.baseData
		}
	}
	// Defensive fallback: physicalSlots should make this unreachable for a
	// healthy lane, but preserving Client-level serialization is safer than
	// converting a scheduler race into a false transport failure.
	for off := 0; off < n; off++ {
		i := (start + off) % n
		if i < len(m.writerDead) && !m.writerDead[i] && m.writers[i] != nil {
			return i, m.writers[i], false, m.baseData
		}
	}
	return -1, nil, false, m.baseData
}

func (m *laneMail) releaseDataWriter(index int, writer *mailbox.Client, reserved bool) {
	if !reserved {
		return
	}
	m.writerMu.Lock()
	if index >= 0 && index < len(m.writers) && m.writers[index] == writer && index < len(m.writerBusy) {
		m.writerBusy[index] = false
	}
	m.writerMu.Unlock()
}

func (m *laneMail) AppendData(ctx context.Context, f string, raw []byte) error {
	return m.AppendDataPriority(ctx, f, raw, false)
}
func (m *laneMail) AppendDataPriority(ctx context.Context, f string, raw []byte, fast bool) error {
	if fast && m.topologyCaps&capSplitData != 0 && len(raw) <= 64<<10 && !strings.Contains(f, "-data-") {
		// This is charged to DATA, including unknown outcomes. Only its physical
		// service uses the control socket; large payloads cannot enter this slot.
		return m.appendControlData(ctx, workUrgent, f, raw)
	}
	if fast && strings.Contains(f, "-data-") && m.dataReaderWake != nil {
		return m.appendShortData(ctx, f, raw)
	}
	if m.runtime != nil {
		return m.runtime.AppendClass(ctx, m.runtimeUser, f, raw, fast)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		index, writer, reserved, base := m.reserveDataWriter()
		if writer != nil {
			if !writer.Ready() {
				m.retireDataWriter(index, writer)
				m.releaseDataWriter(index, writer, reserved)
				continue
			}
			err := m.command(ctx, workData, func() error { return writer.Append(ctx, f, raw) })
			if err != nil {
				// mailbox.Client closes a connection after an operation error. Never
				// round-robin back onto that poisoned DATA socket: doing so turns one
				// provider EOF into a sequence of immediate UNKNOWN outcomes that can
				// cascade across striped owners. Quarantine exactly this writer and
				// repair DATA capacity asynchronously with paced redials.
				m.retireDataWriter(index, writer)
			}
			m.releaseDataWriter(index, writer, reserved)
			if errors.Is(err, mailbox.ErrNotStarted) {
				continue
			}
			return err
		}
		if base {
			class := workData
			if fast {
				class = workUrgent
			}
			return m.appendControlData(ctx, class, f, raw)
		}
		// Capacity repair owns replacement. Await it only while this lane is live;
		// no raw bytes were APPENDed and no unknown publication is resent.
		m.writerMu.Lock()
		alive := !m.closed && m.dataCtx != nil && m.dataCtx.Err() == nil && m.repairing
		m.writerMu.Unlock()
		if !alive {
			return errors.New("DATA_WRITER_UNAVAILABLE")
		}
		if err := pause(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}

// A capped endpoint can use control as its only DATA writer. Repair a known
// dead socket before invoking APPEND; otherwise a burst consumes whole grants
// as immediate UNKNOWN attempts on the same dead connection before the control
// writer repairs it. Once Append is invoked its result is returned exactly once:
// an uncertain publication is never retried here with the same MIME/counters.
func (m *laneMail) appendControlData(ctx context.Context, class accountWorkClass, folder string, raw []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !m.controlClient().Ready() {
			if err := m.repairControl(ctx); err != nil {
				return err
			}
		}
		notStarted := false
		err := m.controlCommand(ctx, class, func() error {
			writer := m.controlClient()
			if !writer.Ready() {
				notStarted = true
				return mailbox.ErrNotStarted
			}
			return writer.Append(ctx, folder, raw)
		})
		if !notStarted {
			return err
		}
		// The socket died while this caller queued for admission. No APPEND
		// call happened, so release admission and repair before trying it.
	}
}

func (m *laneMail) retireDataWriter(index int, writer *mailbox.Client) {
	m.writerMu.Lock()
	if m.closed || index < 0 || index >= len(m.writers) || m.writers[index] != writer || m.writerDead[index] {
		m.writerMu.Unlock()
		_ = writer.Close()
		return
	}
	m.writerDead[index] = true
	startRepair := m.startDataRepairLocked()
	wake := m.dataWake
	m.writerMu.Unlock()
	_ = writer.Close()
	if wake != nil {
		wake()
	}
	if startRepair {
		go m.repairDataWriters()
	}
}

// writerMu also serializes repair admission with Close's cancel/join barrier.
func (m *laneMail) startDataRepairLocked() bool {
	if m.closed || m.repairing || m.dataCtx == nil || m.dataCtx.Err() != nil || m.dial == nil {
		return false
	}
	for _, dead := range m.writerDead {
		if dead {
			m.repairing = true
			m.repairWG.Add(1)
			return true
		}
	}
	return false
}

// repairDataWriters serializes replacement dials per account/lane. Public IMAP
// providers often rate-limit bursts, so a failed DATA connection is retried with
// exponential pacing instead of reconnecting both writers at once. Control and
// reader connections remain untouched while DATA capacity recovers.
func (m *laneMail) repairDataWriters() {
	defer m.repairWG.Done()
	delay := time.Second
	attempted := false
	for {
		m.writerMu.Lock()
		ctx := m.dataCtx
		index := -1
		for i, dead := range m.writerDead {
			if dead && i < len(m.writers) {
				index = i
				break
			}
		}
		if index < 0 || ctx == nil || ctx.Err() != nil {
			m.repairing = false
			m.writerMu.Unlock()
			return
		}
		m.writerMu.Unlock()

		// At least one DATA writer is required to make existing application
		// streams schedulable. Restore that minimum service with repair priority;
		// extra parallel writers remain optional and keep ordinary admission.
		dialCtx := ctx
		m.writerMu.Lock()
		haveWriter := false
		for i, writer := range m.writers {
			if writer != nil && i < len(m.writerDead) && !m.writerDead[i] && writer.Ready() {
				haveWriter = true
				break
			}
		}
		m.writerMu.Unlock()
		if !haveWriter {
			dialCtx = repairAdmission(ctx)
		}
		// Do not add an unconditional one-second blackout when every DATA
		// writer was lost. The first minimum-service dial is immediate; failed
		// retries and optional extra capacity retain their existing backoff.
		if haveWriter || attempted {
			if dataRepairPause(ctx, m.dialGate.repairDelay(delay)) != nil {
				m.writerMu.Lock()
				m.repairing = false
				m.writerMu.Unlock()
				return
			}
		}
		attempted = true
		fresh, err := m.dial(dialCtx)
		if err != nil {
			delay = nextDataRepairDelay(delay)
			continue
		}
		delay = time.Second
		m.writerMu.Lock()
		if index < len(m.writers) && index < len(m.writerDead) && m.writerDead[index] && ctx.Err() == nil {
			m.writers[index] = fresh
			m.writerDead[index] = false
			m.baseData = false
			if index < len(m.writerBusy) {
				m.writerBusy[index] = false
			}
			wake := m.dataWake
			m.writerMu.Unlock()
			m.watchDataWriter(index, fresh)
			if wake != nil {
				wake()
			}
			continue
		}
		m.writerMu.Unlock()
		_ = fresh.Close()
	}
}

func (m *laneMail) SetDataWake(wake func()) {
	m.writerMu.Lock()
	defer m.writerMu.Unlock()
	if m.closed {
		return
	}
	m.dataWake = wake
	if m.runtime != nil {
		m.runtime.setCapacityWake(m, wake)
	}
}

func (m *laneMail) DataAvailable() bool {
	if m.runtime != nil {
		return m.runtime.ctx.Err() == nil && m.runtime.availableWorkers() > 0
	}
	m.writerMu.Lock()
	defer m.writerMu.Unlock()
	if m.closed {
		return false
	}
	if m.baseData {
		return true
	}
	for i, writer := range m.writers {
		if writer != nil && i < len(m.writerDead) && !m.writerDead[i] && writer.Ready() {
			return true
		}
	}
	return false
}
func (m *laneMail) DataParallelism() int {
	if m.runtime != nil {
		return m.runtime.Parallelism()
	}
	m.writerMu.Lock()
	defer m.writerMu.Unlock()
	live := 0
	for i, writer := range m.writers {
		if writer != nil && i < len(m.writerDead) && !m.writerDead[i] && writer.Ready() {
			live++
		}
	}
	if live > 0 {
		return live
	}
	return 1
}
func (m *laneMail) ReaderIdleCapable() bool {
	reader := m.readerRef()
	return reader != nil && reader.IdleCapable()
}

func (m *laneMail) SetReaderHot(until time.Time) {
	if m == nil {
		return
	}
	extendReaderHot(&m.readerHotUntil, until.UnixNano())
	m.readerHotRequestAt.Store(time.Now().UnixNano())
	m.wakeDataReader()
}

// ReaderWakeToFetchDelay consumes a best-effort diagnostic timestamp. It never
// alters reader readiness, IDLE wakeups, quotas or mailbox cursors.
func (m *laneMail) ReaderWakeToFetchDelay() time.Duration {
	if m == nil {
		return -1
	}
	sent := m.readerHotRequestAt.Swap(0)
	if sent <= 0 {
		return -1
	}
	elapsed := time.Duration(time.Now().UnixNano() - sent)
	if elapsed < 0 || elapsed > 10*time.Second {
		return -1
	}
	return elapsed
}

// WaitRead uses IMAP IDLE only on the dedicated receive connection. Control,
// DATA and cleanup keep their existing independent sockets, so a long wait for
// peer mail cannot serialize application writes. The boolean result reports
// whether IDLE was actually used; providers without the extension retain the
// proven gate13 polling fallback.
func (m *laneMail) WaitRead(ctx context.Context, f string, maxWait time.Duration) (used, notified bool, err error) {
	if m != nil && m.recv == "" && m.readerRef() == nil && f == m.handshakeRecv && m.controlClient().IdleCapable() {
		notified, err = m.controlClient().IdleWait(ctx, f, maxWait)
		return true, notified, err
	}
	used, notified, _, err = m.receiveWait(ctx, f, maxWait, nil)
	return

}

func (m *laneMail) WaitReadWake(ctx context.Context, f string, maxWait time.Duration, wake <-chan struct{}) (bool, bool, bool, error) {
	return m.receiveWait(ctx, f, maxWait, wake)
}

func (m *laneMail) Tail(ctx context.Context, f string) (mailbox.Cursor, error) {
	m.readerCommandMu.Lock()
	defer m.readerCommandMu.Unlock()
	reader := m.readerRef()
	if reader != nil && f == m.recv {
		return reader.Tail(ctx, f)
	}
	return m.controlClient().Tail(ctx, f)
}
func (m *laneMail) Recent(ctx context.Context, f string, limit int) ([]mailbox.Message, mailbox.Cursor, error) {
	m.readerCommandMu.Lock()
	defer m.readerCommandMu.Unlock()
	reader := m.readerRef()
	if reader != nil && f == m.recv {
		return reader.Recent(ctx, f, limit)
	}
	return m.controlClient().Recent(ctx, f, limit)
}
func freshReaderSelection(expected, seen uint32, checked, now time.Time) bool {
	age := now.Sub(checked)
	return expected != 0 && seen == expected && age >= 0 && age < 2*time.Second
}

func (m *laneMail) Page(ctx context.Context, f string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	return m.page(ctx, f, cur, nil)
}

func (m *laneMail) PageStream(ctx context.Context, f string, cur mailbox.Cursor, emit func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error) {
	return m.page(ctx, f, cur, emit)
}

func (m *laneMail) page(ctx context.Context, f string, cur mailbox.Cursor, emit func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error) {
	m.readerCommandMu.Lock()
	defer m.readerCommandMu.Unlock()
	primary := m.readerRef()
	if primary == nil && m.recv == "" && f == m.handshakeRecv {
		verify := cur.Validity == 0 || m.readerCheck.IsZero() || time.Since(m.readerCheck) >= 2*time.Second
		rows, next, err := m.controlClient().PageTailLimit(ctx, f, cur, verify, 16)
		if err == nil && verify {
			m.readerCheck = time.Now()
		}
		return rows, next, err
	}
	if primary != nil && f == m.recv {
		verify := cur.Validity == 0 || m.readerCheck.IsZero() || time.Since(m.readerCheck) >= 2*time.Second
		var observedUIDNext uint32
		if validity, uidNext, checked, ok := primary.SelectionAllocationProof(f); ok && freshReaderSelection(cur.Validity, validity, checked, time.Now()) {
			observedUIDNext = uidNext
		}
		// Amortize the receive round trip across DATA and control, with one
		// bounded command. A one-row page cannot drain both active classes.
		limit := m.readerPages.size(time.Now().UnixNano() < m.readerHotUntil.Load())
		readStarted := time.Now()
		largestMIME := 0
		callback := emit
		if emit != nil {
			callback = func(row mailbox.Message) error {
				largestMIME = max(largestMIME, len(row.MIME))
				return emit(row)
			}
		}
		reader, rows, next, err := m.readPage(ctx, f, cur, verify, limit, callback)
		if err == nil {
			if emit == nil {
				for _, row := range rows {
					largestMIME = max(largestMIME, len(row.MIME))
				}
			}
			baseline := time.Duration(m.providerRTT.Load())
			if verify && cur.Validity == 0 {
				baseline *= 2
			}
			m.readerPages.observeWithRTT(limit, len(rows), time.Since(readStarted), baseline)
			m.readerPages.observeBodyShape(len(rows), largestMIME)
		}
		if err == nil && verify && reader != nil {
			m.readerCheck = time.Now()
		}
		if err == nil && reader != nil && observedUIDNext != 0 {
			next = readerAllocatedNext(cur, next, observedUIDNext, limit)
			reader.TrackTailGaps(f, cur, next, rows)
		}
		return rows, next, err
	}
	var rows []mailbox.Message
	var next mailbox.Cursor
	// With a socket cap, control receive shares the maintenance connection.
	// Use the bounded live-tail path here too. It checks SELECT after any
	// folder switch, and periodically verifies UIDVALIDITY on an unchanged
	// selection; maintenance inventory retains its conservative Page path.
	read := func() error {
		var e error
		if f == m.recv && m.recv != "" {
			verify := cur.Validity == 0 || m.readerCheck.IsZero() || time.Since(m.readerCheck) >= 2*time.Second
			rows, next, e = m.cleanupClient().PageTailLimit(ctx, f, cur, verify, 16)
			if e == nil && verify {
				m.readerCheck = time.Now()
			}
		} else {
			rows, next, e = m.cleanupClient().Page(ctx, f, cur)
		}
		return e
	}
	readCommand := func() error {
		if f == m.recv && m.recv != "" {
			// A socket cap changes the physical connection, not the class of
			// live receive work. Sharing maintenance's account admission queue
			// can strand OPEN_OK/PONG behind unrelated proof work even while
			// this socket is idle. Actual bytes still take a fair baseWork turn.
			if m.cleanupClient() == m.controlClient() {
				return m.controlReceiveCommand(ctx, read)
			}
			return m.command(ctx, workReceive, read)
		}
		return m.maintenanceCommand(ctx, read)
	}
	err := readCommand()
	// With a provider socket cap, receive may share the control connection.
	// Retry only this read, keeping its original cursor. APPEND and deletion
	// outcomes never pass through this read-only retry path.
	if err != nil && m.recv != "" && retryableReceive(err) && ctx.Err() == nil && m.cleanupClient() == m.controlClient() {
		if repairErr := m.repairControl(ctx); repairErr != nil {
			return nil, cur, repairErr
		}
		m.readerCheck = time.Time{}
		err = readCommand()
	}
	return rows, next, err
}

// A successful bounded FETCH plus a matching fresh SELECT snapshot can pass
// already allocated holes. Stop at the end of that fetched range, never at a
// future UID and never on an error or changed incarnation.
func readerAllocatedNext(cur, next mailbox.Cursor, uidNext, limit uint32) mailbox.Cursor {
	if cur.Validity == 0 || cur.Validity != next.Validity || uidNext == 0 || limit < 1 || limit > 16 || cur.After == ^uint32(0) {
		return next
	}
	last := cur.After + limit
	if last < cur.After {
		last = ^uint32(0)
	}
	through := min(uidNext-1, last)
	if through > next.After {
		next.After = through
	}
	return next
}

// Maintenance operations use the optional cleaner connection so a long
// SELECT/FETCH/STORE/EXPUNGE proof cannot head-of-line block UsageReport, OPEN
// or grant control APPENDs on the base connection.
func (m *laneMail) cleanupClient() *mailbox.Client {
	m.cleanerMu.Lock()
	cleaner := m.cleaner
	m.cleanerMu.Unlock()
	if cleaner != nil {
		return cleaner
	}
	return m.controlClient()
}

func (m *laneMail) RepairCleanup(ctx context.Context) error {
	m.cleanerMu.Lock()
	old := m.cleaner
	m.cleaner = nil
	if m.dial == nil || m.cleanerRepairing || ctx.Err() != nil {
		m.cleanerMu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		return ctx.Err()
	}
	m.cleanerRepairing = true
	m.repairWG.Add(1)
	m.cleanerMu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	// Cleaner repair is deliberately asynchronous. A maintenance socket is an
	// isolation optimization, not part of the application data/control liveness
	// contract. Dialing it synchronously from the lane actor would turn one slow
	// provider reconnect into a fresh multi-second stall for OPEN/WINDOW/PING.
	go func(parent context.Context) {
		defer m.repairWG.Done()
		delay := 250 * time.Millisecond
		for parent.Err() == nil {
			if pause(parent, delay) != nil {
				break
			}
			dialCtx, cancel := context.WithTimeout(parent, 5*time.Second)
			fresh, err := m.dial(dialCtx)
			cancel()
			if err == nil {
				m.cleanerMu.Lock()
				if m.cleaner == nil && parent.Err() == nil {
					m.cleaner = fresh
					fresh = nil
				}
				m.cleanerRepairing = false
				m.cleanerMu.Unlock()
				if fresh != nil {
					_ = fresh.Close()
				}
				return
			}
			if delay < 30*time.Second {
				delay *= 2
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
			}
		}
		m.cleanerMu.Lock()
		m.cleanerRepairing = false
		m.cleanerMu.Unlock()
	}(ctx)
	return nil
}

func (m *laneMail) DeleteMany(ctx context.Context, messages []mailbox.Message) error {
	return m.maintenanceCommand(ctx, func() error { return m.cleanupClient().DeleteMany(ctx, messages) })
}
func (m *laneMail) DeleteObservedMany(ctx context.Context, messages []mailbox.Message) error {
	return m.maintenanceCommand(ctx, func() error { return m.cleanupClient().DeleteObservedMany(ctx, messages) })
}
func (m *laneMail) Delete(ctx context.Context, message mailbox.Message) error {
	return m.maintenanceCommand(ctx, func() error { return m.cleanupClient().Delete(ctx, message) })
}
func (m *laneMail) Absent(ctx context.Context, folder string, validity, uid uint32) (bool, error) {
	var absent bool
	err := m.maintenanceCommand(ctx, func() error { var e error; absent, e = m.cleanupClient().Absent(ctx, folder, validity, uid); return e })
	return absent, err
}
func (m *laneMail) Present(ctx context.Context, folder string, validity uint32, ids []uint32) (map[uint32]bool, error) {
	var present map[uint32]bool
	err := m.maintenanceCommand(ctx, func() error {
		var e error
		present, e = m.cleanupClient().Present(ctx, folder, validity, ids)
		return e
	})
	return present, err
}

func (m *laneMail) Close() error {
	m.once.Do(func() {
		m.writerMu.Lock()
		m.closed = true
		if m.runtime != nil {
			m.runtime.setCapacityWake(m, nil)
		}
		if m.dataCancel != nil {
			m.dataCancel()
		}
		m.writerMu.Unlock()
		_ = m.controlClient().Close()
		m.topologyMu.Lock()
		if m.dataReader != nil {
			_ = m.dataReader.Close()
			m.dataReader = nil
		}
		m.topologyMu.Unlock()
		if reader := m.readerRef(); reader != nil {
			_ = reader.Close()
		}
		m.cleanerMu.Lock()
		if m.cleaner != nil {
			_ = m.cleaner.Close()
			m.cleaner = nil
		}
		m.cleanerMu.Unlock()
		m.writerMu.Lock()
		for _, writer := range m.writers {
			if writer != nil {
				_ = writer.Close()
			}
		}
		m.writers = nil
		m.writerDead = nil
		m.writerBusy = nil
		m.writerMu.Unlock()
		m.repairWG.Wait()
	})
	return nil
}

type pageResult struct {
	rows []mailbox.Message
	err  error
}

// wakeReader interrupts either synthetic empty-mailbox backoff or the current
// dedicated-reader IMAP IDLE command. IDLE is ended with DONE, not by closing the
// socket, so a PING/OPEN can force an immediate tail check without creating a
// reconnect. A one-element channel coalesces bursts of latency-critical control.
func (l *lane) wakeReader() {
	if l == nil || l.readWake == nil {
		return
	}
	select {
	case l.readWake <- struct{}{}:
	default:
	}
}

func readerPause(ctx context.Context, wake <-chan struct{}, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	if wake == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wake:
		return nil
	case <-t.C:
		return nil
	}
}

func readerPollDelay(active int64, hot bool, advanced bool, rows int, idle *int) time.Duration {
	if advanced || rows > 0 {
		*idle = 0
		// While a UID tail is actually advancing, drain it without an artificial
		// sleep. The pacing below applies only to empty polls.
		return 0
	}
	if hot {
		*idle = min(*idle+1, 4)
		// A response hint bounds latency, but is not evidence of visible mail.
		// Do not spin an empty account at 500 FETCH/s until the hint expires.
		return time.Duration(10<<(*idle-1)) * time.Millisecond
	}
	if active > 0 {
		// Active TCP streams are not evidence that this account has new mail.
		// Bound empty FETCH pressure on unused siblings; actual rows reset this
		// delay and request-like control wakes the reader immediately.
		steps := [...]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}
		index := min(*idle, len(steps)-1)
		if *idle < len(steps) {
			*idle++
		}
		return steps[index]
	}
	steps := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, 1500 * time.Millisecond, 2 * time.Second}
	index := *idle
	if index >= len(steps) {
		index = len(steps) - 1
	} else {
		*idle++
	}
	return steps[index]
}

const readerIdleAudit = 2 * time.Second

// The first audits immediately after a latency-critical response search should
// not inherit the full quiet 2s/active 750ms IDLE timeout. Provider EXISTS may
// lag the physical APPEND. Bound the extra checking to 700ms after the hint
// expires; ordinary idle accounts keep the existing IDLE cadence.
const (
	readerResponseAuditGrace = 700 * time.Millisecond
	readerResponseAuditDelay = 150 * time.Millisecond
)

func readerIdleAuditDelay(active int64, hotDeadline int64, now time.Time) time.Duration {
	base := readerIdleAudit
	if active > 0 {
		base = 500 * time.Millisecond
	}
	if hotDeadline <= 0 {
		return base
	}
	age := now.UnixNano() - hotDeadline
	if age >= 0 && age < int64(readerResponseAuditGrace) {
		return readerResponseAuditDelay
	}
	return base
}

func (l *lane) startReader() {

	l.workers.Add(1)
	go func() {
		defer l.workers.Done()
		cur := l.recvCursor
		idle := 0
		var notificationPolicy notificationAuditPolicy
		demand := responseWaitPolicy{boundedPoll: true}
		var hotGeneration uint64
		for l.ctx.Err() == nil {
			// A fresh request starts a new bounded response search. Bulk/GC
			// wakes do not reset empty pacing, and the steady hot cap is unchanged.
			if generation := l.readHotGeneration.Load(); generation != hotGeneration {
				hotGeneration = generation
				idle = 0
				demand.request()
			}
			var rows []mailbox.Message
			var next mailbox.Cursor
			var err error
			streamed := false
			if reader, ok := l.mail.(interface {
				PageStream(context.Context, string, mailbox.Cursor, func(mailbox.Message) error) ([]mailbox.Message, mailbox.Cursor, error)
			}); ok {
				rows, next, err = reader.PageStream(l.ctx, l.recvFolder, cur, func(m mailbox.Message) error {
					streamed = true
					select {
					case l.pages <- pageResult{rows: []mailbox.Message{m}}:
						return nil
					case <-l.ctx.Done():
						return l.ctx.Err()
					}
				})
			} else {
				rows, next, err = l.mail.Page(l.ctx, l.recvFolder, cur)
			}
			if err == nil && cur.Validity != 0 && cur.Validity != next.Validity && l.diag != nil {
				// Folder recreation invalidates physical locators but does not invalidate
				// the authenticated logical session. Continue from the new incarnation;
				// missing logical offsets are repaired by normal progress/NACK handling.
				l.diag.event("event=reader_uidvalidity_reset")
			}
			if !streamed || err != nil {
				finalRows := rows
				if streamed {
					finalRows = nil
				}
				select {
				case l.pages <- pageResult{finalRows, err}:
				case <-l.ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
			advanced := next != cur
			notificationPolicy.fetched(len(rows), time.Now())
			cur = next
			hot := time.Now().UnixNano() < l.readHotUntil.Load()
			if !hot && l.stripe != nil {
				hot = l.stripe.parallelFastTraffic()
			}
			directRTT := time.Duration(0)
			if provider, ok := l.mail.(interface{ ReaderDirectRTT() time.Duration }); ok {
				directRTT = provider.ReaderDirectRTT()
			}
			idleCapable := false
			if provider, ok := l.mail.(interface{ ReaderIdleCapable() bool }); ok {
				idleCapable = provider.ReaderIdleCapable()
			}
			// ReaderIdleCapable requires a dedicated receive connection.
			// Shared DATA/cleanup writers do not make this independent reader
			// unsafe to poll during a bounded request-response window.
			adaptive := idleCapable && demand.enabled(directRTT, true)
			useDemandIdle := adaptive && demand.empty(hot, advanced, len(rows))
			active := l.active.Load()
			if l.stripe != nil && l.stripe.activeStreams() > active {
				active = l.stripe.activeStreams()
			}
			// Once the bounded UID tail is drained, prefer event-driven IMAP IDLE on
			// the dedicated reader. During a short latency-critical response window,
			// however, stay out of IDLE and use the proven fast bounded poll cadence.
			// A PING/OPEN queued while IDLE is already active closes that IDLE with DONE
			// through readWake, causing an immediate tail check without reconnecting the
			// reader socket. The bounded audit remains the quiet-lane safety net.
			// An open TCP flow is not evidence of new mail on every account.
			// After two empty active polls, wait for the provider notification;
			// retain a short audit for delayed visibility and missing notifications.
			if (!hot && !advanced && len(rows) == 0 && (active == 0 || idle >= 2)) || useDemandIdle {
				audit := readerIdleAuditDelay(active, l.readHotUntil.Load(), time.Now())
				audit = notificationPolicy.delay(active, audit, time.Now())
				if useDemandIdle {
					audit = min(audit, demand.audit(directRTT))
				}
				if waiter, ok := l.mail.(interface {
					WaitReadWake(context.Context, string, time.Duration, <-chan struct{}) (bool, bool, bool, error)
				}); ok {
					used, notified, kicked, waitErr := waiter.WaitReadWake(l.ctx, l.recvFolder, audit, l.readWake)
					if waitErr != nil {
						select {
						case l.pages <- pageResult{err: waitErr}:
						case <-l.ctx.Done():
						}
						return
					}
					if used {
						notificationPolicy.waited(notified, kicked)
						// Stay event-driven until a row/hot request resets the streak.
						idle = 2
						continue
					}
				} else if waiter, ok := l.mail.(interface {
					WaitRead(context.Context, string, time.Duration) (bool, bool, error)
				}); ok {
					used, notified, waitErr := waiter.WaitRead(l.ctx, l.recvFolder, audit)
					if waitErr != nil {
						select {
						case l.pages <- pageResult{err: waitErr}:
						case <-l.ctx.Done():
						}
						return
					}
					if used {
						notificationPolicy.waited(notified, false)
						idle = 2
						continue
					}
				}
			}
			delay := readerPollDelay(active, hot, advanced, len(rows), &idle)
			if adaptive && hot && !advanced && len(rows) == 0 {
				delay = min(delay, demand.poll(directRTT))
			}
			if delay > 0 && readerPause(l.ctx, l.readWake, delay) != nil {
				return
			}
		}
	}()
}

// Dial helpers keep partial activation cleanup and socket ownership explicit.
func openLaneMail(ctx context.Context, dial Dial, cfg mailbox.Config, folders []string) (*laneMail, error) {
	return openLaneMailWithGate(ctx, dial, cfg, folders, newAccountDialGate())
}
func openLaneMailWithGate(ctx context.Context, dial Dial, cfg mailbox.Config, folders []string, gate *accountDialGate) (*laneMail, error) {
	scoped := func(ctx context.Context) (client *mailbox.Client, err error) {
		err = gate.run(ctx, func() error { client, err = dial(ctx, cfg, folders); return err })
		return client, err
	}
	c, err := scoped(controlAdmission(ctx))
	if err != nil {
		return nil, err
	}
	return &laneMail{Client: c, dial: scoped, dialGate: gate}, nil
}

// openLaneMailFastStart changes only the initial client-side optional DATA
// socket: after one healthy writer, it schedules the second through existing
// paced repair. Server shared runtimes and stand-alone mailbox tests retain
// their original eager activation. The first writer, reader, and cleaner
// remain mandatory as before, and the existing connection-cap fallback stays.
func openLaneMailFastStart(ctx context.Context, dial Dial, cfg mailbox.Config, folders []string) (*laneMail, error) {
	gate := newAccountDialGate()
	gate.slot = &loginAdmission{limit: 2}
	m, err := openLaneMailWithGate(ctx, dial, cfg, folders, gate)
	if err == nil {
		m.stageSecondDataWriter = true
	}
	return m, err
}

func openLaneMailShared(ctx context.Context, dial Dial, cfg mailbox.Config, folders []string, runtime *accountRuntime, user string) (*laneMail, error) {
	gate := newAccountDialGate()
	if runtime != nil {
		gate = runtime.gate
	}
	m, err := openLaneMailWithGate(ctx, dial, cfg, folders, gate)
	if err != nil {
		return nil, err
	}
	m.runtime, m.runtimeUser = runtime, user
	return m, nil
}

func (m *observedMail) Activate(ctx context.Context, recv string) error {
	if split, ok := m.Transport.(interface {
		Activate(context.Context, string) error
	}); ok {
		err := m.op("activate_io", func() error { return split.Activate(ctx, recv) })
		if io, ok := m.Transport.(*laneMail); ok && err == nil {
			n := io.connectionCount()
			readerIdle := io.ReaderIdleCapable()
			parallel := io.DataParallelism()
			m.d.mu.Lock()
			m.d.line.IOConnections = n
			m.d.line.ReaderIdle = readerIdle
			m.d.mu.Unlock()
			m.d.event(fmt.Sprintf("event=io_mode lane_connections=%d data_parallel=%d reader_isolated=%t reader_idle=%t cleanup_isolated=%t fallback=%s", n, parallel, io.readerRef() != nil, readerIdle, io.cleaner != nil, io.modeError))
			m.d.event(fmt.Sprintf("event=append_capability mode=%s binary=%t", io.controlClient().AppendLiteralMode(), io.controlClient().BinaryAppendEnabled()))
			m.d.event(fmt.Sprintf("event=fetch_capability binary=%t", io.ReaderBinaryFetchEnabled()))
		}
		return err
	}
	return nil
}

// Include shared account DATA sockets and exclude observed dead generations.
func (m *laneMail) connectionCount() int {
	seen := map[uint64]bool{}
	for _, s := range m.SocketSnapshots() {
		if s.State != "dead" {
			seen[s.ID] = true
		}
	}
	return len(seen)
}

var _ mailbox.Transport = (*laneMail)(nil)

// Some providers impose a low connection limit. Existing authenticated TLS
// connections remain usable; never downgrade TLS or swallow cancellation.
func connectionLimitFallback(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return err != nil && (errors.Is(err, errLocalConnectionLimit) || err.Error() == "IMAP_CONNECT_FAILED" || err.Error() == "IMAP_AUTH_OR_GREETING_FAILED")
}

// FetchCleanupCandidates uses APPENDUID solely to discover bodies. No journal
// balance changes here, and wrong/stale hints cannot authorize a deletion.
func (m *laneMail) FetchCleanupCandidates(ctx context.Context, folder string, wanted map[[32]byte]bool) ([]mailbox.Message, error) {
	hints := m.controlClient().AppendHints(folder, wanted)
	m.borrowHintMu.Lock()
	for _, h := range m.borrowHints {
		if h.Folder == folder && wanted[h.Digest] {
			hints = append(hints, h)
		}
	}
	m.borrowHintMu.Unlock()
	if m.runtime != nil {
		m.runtime.mu.Lock()
		for _, entry := range m.runtime.appendHints {
			if entry.user == m.runtimeUser && entry.Folder == folder && wanted[entry.Digest] {
				hints = append(hints, entry.AppendHint)
			}
		}
		m.runtime.mu.Unlock()
	} else {
		m.writerMu.Lock()
		writers := append([]*mailbox.Client(nil), m.writers...)
		m.writerMu.Unlock()
		for _, writer := range writers {
			if writer != nil && writer != m.controlClient() {
				hints = append(hints, writer.AppendHints(folder, wanted)...)
			}
		}
	}
	if len(hints) == 0 {
		return nil, nil
	}
	validity := hints[0].Validity
	ids := make([]uint32, 0, 32)
	seen := map[uint32]bool{}
	var candidateBytes uint64
	for _, hint := range hints {
		if hint.Validity == validity && !seen[hint.UID] {
			size := hint.MIMEBytes
			if size == 0 {
				size = mailbox.MaxMIME
			}
			if len(ids) > 0 && candidateBytes+size > 2<<20 {
				break
			}
			candidateBytes += size
			ids = append(ids, hint.UID)
			seen[hint.UID] = true
		}
		if len(ids) == 32 {
			break
		}
	}
	var rows []mailbox.Message
	err := m.maintenanceCommand(ctx, func() error {
		var e error
		rows, e = m.cleanupClient().FetchExact(ctx, folder, validity, ids)
		return e
	})
	if errors.Is(err, mailbox.ErrChanged) {
		return nil, nil
	}
	return rows, err
}

func extendReaderHot(value *atomic.Int64, until int64) {
	for {
		old := value.Load()
		if old >= until {
			return
		}
		if value.CompareAndSwap(old, until) {
			return
		}
	}
}

func (m *laneMail) ReaderCommandCounts() (uint64, uint64, uint64) {
	if reader := m.readerRef(); reader != nil {
		return reader.ReadCommandCounts()
	}
	return m.controlClient().ReadCommandCounts()
}
func (m *laneMail) ReaderBinaryFetchEnabled() bool {
	if reader := m.readerRef(); reader != nil {
		return reader.BinaryFetchEnabled()
	}
	return m.controlClient().BinaryFetchEnabled()
}

func (m *laneMail) PrepareHandshake(folder string) {
	m.handshakeRecv = folder
	m.readerCheck = time.Time{}
}

func (m *observedMail) PrepareHandshake(folder string) {
	if p, ok := m.Transport.(interface{ PrepareHandshake(string) }); ok {
		p.PrepareHandshake(folder)
	}
}

func (m *observedMail) Ping(ctx context.Context) (time.Duration, error) {
	if p, ok := m.Transport.(interface {
		Ping(context.Context) (time.Duration, error)
	}); ok {
		return p.Ping(ctx)
	}
	return 0, nil
}

// ReaderDirectRTT is an unloaded provider command measurement. Loaded tunnel
// echoes must not inflate propagation or shorten transport recovery deadlines.
func (m *laneMail) ReaderDirectRTT() time.Duration { return time.Duration(m.providerRTT.Load()) }
