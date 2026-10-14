package datapath

import (
	"context"
	"errors"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

// Input is emitted by an authenticated session mailbox adapter, never directly
// by an untrusted MIME parser. Exactly one of Frame / Cleaned is populated.
// Cleaned requires verified physical GC, not a logical RECEIPT.
type Input struct {
	Generation uint64
	Frame      *wire.Frame
	Cleaned    uint64
}

// Link owns Noise/mail/quota policy; Pump exclusively owns the byte Engine.
// Receive MUST obey context and transfer ownership of Frame.Payload. It must
// reserve capacity before consuming replay state and must not silently drop an
// authenticated record when its downstream queue is full. Size must be pure.
// Write invokes the existing durable Job.WriteRemote or Job.WriteLocal exactly
// once; all I/O methods must terminate on context cancellation (Job uses the
// engine context). A link MUST NOT claim physical GC after SendControl alone.
type Link interface {
	Receive(context.Context) (Input, error)
	SendControl(context.Context, wire.Frame) error
	Size(wire.Frame) (uint64, error)
	Write(*Job) Completion
}

// BatchLink is an optional extension used by the real IMAP tunnel. The base
// Link remains intentionally small so deterministic unit fixtures can keep a
// single-job writer. A batch writer must return exactly one completion per job
// in input order and must represent one physical APPEND outcome for the batch.
type BatchLink interface {
	WriteBatch([]*Job) []Completion
}

// ParallelBatchLink opts into a small number of concurrent physical batch
// writers. The Pump keeps accounting serialized in its actor; only the slow
// physical APPEND operation runs concurrently. Implementations must preserve
// the durable-before-APPEND and exact-once Completion contracts per batch.
type BulkPreparationLink interface {
	AcquireBulkPreparation(context.Context) (func(), error)
}

type ParallelBatchLink interface {
	BatchLink
	BatchParallelism() int
}

// FastBatchLink optionally reserves a small number of scheduler workers for
// latency-sensitive DATA. These workers consume only the short-record queue;
// they do not increase the link's physical APPEND concurrency unless the link
// itself admits it. This prevents all ordinary batch workers from being parked
// inside long bulk bookkeeping/APPEND calls while an interactive record waits
// in dataFast with no goroutine available to pick it up.
type FastBatchLink interface {
	BatchLink
	FastBatchParallelism() int
}

const (
	// InteractiveDataThreshold is shared with the physical-mail adapter so
	// short DATA gets both scheduler priority and a reserved storage permit.
	InteractiveDataThreshold = 8 << 10
	// 336 KiB deliberately crosses to eleven full 32 KiB DATA records while
	// the conservative MIME-bound regression still leaves several KiB below
	// the 512 KiB physical permit. Twelve full records do not fit after the
	// ITD3/Noise/MIME overhead.
	BulkBatchTargetBytes = 336 << 10
	dataBatchMaxRecords  = wire.MaxDataBatchRecords
	dataBatchTargetBytes = BulkBatchTargetBytes
	dataBatchDelay       = 20 * time.Millisecond
	dataFastMaxRecords   = 4
	dataFastTargetBytes  = 64 << 10
	dataFastDelay        = 250 * time.Microsecond
	dataFastThreshold    = InteractiveDataThreshold
	dataFastBurst        = 4
	dataFastQueueRecords = 16
	dataTinyThreshold    = 512
	dataTinyBurst        = 4
	// The tunnel runtime admits a 4 MiB per-account logical window. 128 full-size
	// records cover that already-accounted window and also equal eight physical
	// workers times the 16-record hard batch cap. Queue capacity therefore cannot
	// be the reason later workers emit partial APPENDs under a saturated stream.
	dataBulkQueueRecords = dataBatchMaxRecords * 8
)

type Socket interface {
	net.Conn
	CloseWrite() error
}
type PumpConfig struct {
	// Bulk speculation is bounded independently of the peer's receive credit.
	// The callback supplies logical bytes, never physical publication authority.
	BulkAdmissionLimit        func() uint64
	InteractiveAttemptReserve int
	InteractiveItemReserve    int
	SocketFailure             func(uint64, string, error)
	Acknowledged              func(uint64)
	DeliveryProgress          func(uint64, uint64)
	DeliveryStopped           func(uint64)
	InteractiveReserve        uint64
	Engine                    Config
	Account                   string
	ControlQueue              int
	ReadCoalesce              time.Duration
	Retired                   func(uint64)
}
type addResult struct {
	initial wire.Frame
	err     error
}
type addRequest struct {
	ctx          context.Context
	id           uint64
	socket       Socket
	deferInitial bool
	done         chan addResult
}
type readResult struct {
	bulk bool
	id   uint64
	data []byte
	err  error
	ack  chan struct{}
}
type writeResult struct {
	id   uint64
	n    int
	err  error
	half bool
}
type pumpEvent struct {
	input       *Input
	read        *readResult
	write       *writeResult
	completion  *Completion
	completions []Completion
	err         error
}
type socketState struct {
	socket                        Socket
	read                          *readResult
	bulkRead                      bool
	writes                        chan socketWrite
	writing, localEOF, halfClosed bool
	writeLen                      int
	done                          chan struct{}
}
type socketWrite struct {
	data []byte
	half bool
}

// Pump runs only AFTER Noise, activation, coordinator/wallet binding and OPEN.
// Add transfers an already-authorized, adopted socket to the owner. It is not an
// OPEN implementation and does not authorize arbitrary peer-supplied stream IDs.
type Pump struct {
	scheduler          atomic.Pointer[SchedulerSnapshot]
	awaitingRetirement map[uint64]bool
	cfg                PumpConfig
	link               Link
	ctx                context.Context
	cancel             context.CancelFunc
	done               chan struct{}
	add                chan addRequest
	events             chan pumpEvent
	dataFast           chan *Job
	dataTiny           chan *Job
	fastSelectMu       sync.Mutex
	tinyStreak         int
	dataFastWake       chan struct{}
	dataBulk           chan *Job
	// batchMu serializes only queue aggregation, never physical I/O. Without a
	// single collector, every parallel APPEND worker can steal one 32 KiB DATA
	// record and independently wait for its coalescing timer. That defeats the
	// 336 KiB physical target exactly when parallelism is highest. A worker holds
	// this lock for at most the short coalescing window, releases it before
	// WriteBatch, and the next worker immediately forms the following batch while
	// previous batches are still in flight.
	batchMu         sync.Mutex
	batchFastStreak int
	// fastReserve is true when at least one dedicated short-DATA collector is
	// running. Bulk aggregation may then continue while short DATA is dequeued
	// independently; stripe scheduling gives the fast physical waiter priority.
	fastReserve     bool
	pendingControls pendingControls
	controls        chan wire.Frame
	urgentControls  chan wire.Frame
	workers         sync.WaitGroup
	engine          *Engine
	sockets         map[uint64]*socketState
	attempted       map[uint64]bool
	retryWake       map[uint64]<-chan struct{}
	started         time.Time
	turn            int
	lastDataStream  uint64
	err             error // written before closing done
}

func StartPump(parent context.Context, cfg PumpConfig, link Link) (*Pump, error) {
	if cfg.InteractiveAttemptReserve < 0 || cfg.InteractiveAttemptReserve > cfg.Engine.Limits.Attempts/4 || cfg.InteractiveItemReserve < 0 || cfg.InteractiveItemReserve > cfg.Engine.Limits.Items/4 || cfg.InteractiveReserve > min(cfg.Engine.Limits.Logical, cfg.Engine.ReceiveBytes)/4 || link == nil || cfg.ControlQueue < 2 || cfg.ControlQueue > 1024 || cfg.ReadCoalesce < 0 || cfg.ReadCoalesce > 5*time.Millisecond || cfg.Engine.Limits.Streams > 128 || cfg.Engine.AccountCaps[cfg.Account] == 0 {
		return nil, ErrState
	}
	ctx, cancel := context.WithCancel(parent)
	e, err := New(ctx, cfg.Engine)
	if err != nil {
		cancel()
		return nil, err
	}
	p := &Pump{cfg: cfg, link: link, ctx: ctx, cancel: cancel, done: make(chan struct{}), add: make(chan addRequest), events: make(chan pumpEvent, 2*cfg.Engine.Limits.Streams+8), dataFast: make(chan *Job, dataFastQueueRecords), dataTiny: make(chan *Job, dataFastQueueRecords), dataFastWake: make(chan struct{}, 1), dataBulk: make(chan *Job, dataBulkQueueRecords), controls: make(chan wire.Frame, cfg.ControlQueue), urgentControls: make(chan wire.Frame, min(16, cfg.ControlQueue)), engine: e, sockets: map[uint64]*socketState{}, attempted: map[uint64]bool{}, started: time.Now()}
	p.pendingControls.wake = make(chan struct{}, 1)
	go p.run()
	return p, nil
}
func (p *Pump) Done() <-chan struct{} { return p.done }
func (p *Pump) Wait() error           { <-p.done; return p.err }
func (p *Pump) Close() error          { p.cancel(); return p.Wait() }

// Add closes socket on any rejected request. On success Pump owns it until
// retirement or shutdown. Cancellation after acceptance does not revoke it.
func (p *Pump) Add(ctx context.Context, id uint64, socket Socket) error {
	_, err := p.addSocket(ctx, id, socket, false)
	return err
}

// AddDeferredInitial activates and adopts a socket but returns the initial
// receive WINDOW to the caller instead of publishing it through Link. This is
// used by the tunnel OPEN path to preserve the protocol order OPEN_OK -> WINDOW
// without making application first-byte traffic race an asynchronous Pump
// control worker. The caller MUST queue the returned frame before exposing the
// socket to application code.
func (p *Pump) AddDeferredInitial(ctx context.Context, id uint64, socket Socket) (wire.Frame, error) {
	return p.addSocket(ctx, id, socket, true)
}

func (p *Pump) addSocket(ctx context.Context, id uint64, socket Socket, deferInitial bool) (wire.Frame, error) {
	if socket == nil {
		return wire.Frame{}, ErrState
	}
	req := addRequest{ctx: ctx, id: id, socket: socket, deferInitial: deferInitial, done: make(chan addResult, 1)}
	select {
	case <-ctx.Done():
		socket.Close()
		return wire.Frame{}, ctx.Err()
	case <-p.ctx.Done():
		socket.Close()
		return wire.Frame{}, p.ctx.Err()
	case p.add <- req:
	}
	// Actor acknowledges before selecting again, including on shutdown.
	r := <-req.done
	return r.initial, r.err
}
func (p *Pump) emit(e pumpEvent) bool {
	select {
	case p.events <- e:
		return true
	case <-p.ctx.Done():
		return false
	}
}
func (p *Pump) worker(fn func()) { p.workers.Add(1); go func() { defer p.workers.Done(); fn() }() }
func (p *Pump) control(f wire.Frame) error {
	if reusableControl(f) {
		return p.queueSnapshot(f)
	}
	queue := p.controls
	// A remote DATA batch cannot be decrypted until its authenticated UsageReport
	// descriptor arrives. Let those descriptors bypass bulk WINDOW/CREDIT backlog
	// inside the Pump; lane.SendControl already preserves their urgent treatment
	// after this point. The queue is still bounded and saturation remains fatal.
	if f.Type == wire.GrantUsage && p.urgentControls != nil {
		queue = p.urgentControls
	}
	select {
	case queue <- f:
		return nil
	default:
		return flow.ErrCredit
	}
}

func (p *Pump) nextControl() (wire.Frame, bool) {
	for {
		select {
		case <-p.ctx.Done():
			return wire.Frame{}, false
		default:
		}
		select {
		case f := <-p.urgentControls:
			return f, true
		default:
		}
		select {
		case f := <-p.controls:
			return f, true
		default:
		}
		if f, ok := p.takeSnapshot(); ok {
			return f, true
		}
		select {
		case <-p.ctx.Done():
			return wire.Frame{}, false
		case f := <-p.urgentControls:
			return f, true
		case f := <-p.controls:
			return f, true
		case <-p.pendingControls.wake:
		}
	}
}
func (p *Pump) controlAll(frames []wire.Frame) error {
	for _, f := range frames {
		if e := p.control(f); e != nil {
			return e
		}
	}
	return nil
}
func (p *Pump) run() {
	defer close(p.done)
	defer func() {
		p.cancel()
		p.engine.Stop()
		for _, s := range p.sockets {
			s.socket.Close()
		}
		p.workers.Wait()
		clear(p.sockets)
		clear(p.attempted)
		clear(p.retryWake)
		p.link = nil
		for {
			select {
			case event := <-p.events:
				if event.input != nil && event.input.Frame != nil {
					clear(event.input.Frame.Payload)
				}
			default:
				goto controls
			}
		}
	controls:
		for _, queue := range []chan wire.Frame{p.urgentControls, p.controls} {
			for queue != nil {
				select {
				case f := <-queue:
					clear(f.Payload)
				default:
					queue = nil
				}
			}
		}
		goto jobs
	jobs:
		for _, queue := range []chan *Job{p.dataTiny, p.dataFast, p.dataBulk} {
			for {
				select {
				case job := <-queue:
					clear(job.frame.Payload)
				default:
					goto nextQueue
				}
			}
		nextQueue:
		}
		return
	}()
	p.worker(func() {
		for {
			input, e := p.link.Receive(p.ctx)
			if e != nil {
				p.emit(pumpEvent{err: e})
				return
			}
			if !p.emit(pumpEvent{input: &input}) {
				return
			}
		}
	})
	p.worker(func() {
		for {
			f, ok := p.nextControl()
			if !ok {
				return
			}
			if e := p.link.SendControl(p.ctx, f); e != nil {
				p.emit(pumpEvent{err: e})
				return
			}
		}
	})
	batchLink, batching := p.link.(BatchLink)
	writers := 1
	fastWriters := 0
	if batching {
		if parallel, ok := p.link.(ParallelBatchLink); ok {
			writers = parallel.BatchParallelism()
			if writers < 1 {
				writers = 1
			}
			if writers > 8 {
				writers = 8
			}
		}
		if priority, ok := p.link.(FastBatchLink); ok {
			fastWriters = priority.FastBatchParallelism()
			if fastWriters < 0 {
				fastWriters = 0
			}
			if fastWriters > 2 {
				fastWriters = 2
			}
		}
	}
	p.fastReserve = fastWriters > 0
	for worker := 0; worker < writers; worker++ {
		p.worker(func() { p.writeDataLoop(batchLink, batching) })
	}
	for worker := 0; worker < fastWriters; worker++ {
		p.worker(func() { p.writeFastDataLoop(batchLink) })
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var err error
		select {
		case <-p.ctx.Done():
			p.err = p.ctx.Err()
			return
		case req := <-p.add:
			err = req.ctx.Err()
			var initial wire.Frame
			if err == nil {
				initial, err = p.adopt(req.id, req.socket, req.deferInitial)
			}
			if err != nil {
				req.socket.Close()
			}
			req.done <- addResult{initial: initial, err: err}
			// A capacity/config rejection before activation is local to this Add.
			// A control-queue failure inside adopt is fatal; see adopt's preflight.
			err = nil
		case event := <-p.events:
			err = p.handle(event)
		case <-ticker.C:
			p.captureScheduler()
			var frames []wire.Frame
			frames, err = p.engine.Poll(p.cfg.Engine.Generation, time.Since(p.started))
			for id := range p.sockets {
				if p.engine.Aborted(id) {
					p.dropSocket(id)
				}
			}
			if err == nil {
				err = p.controlAll(frames)
			}
		}
		if err == nil {
			err = p.progress()
		}
		if err != nil {
			p.err = err
			if p.ctx.Err() != nil {
				p.err = p.ctx.Err()
			}
			return
		}
	}
}
func fastDataJob(j *Job) bool {
	return j.Interactive()
}

// Tiny request/reply records get a bounded turn ahead of larger TLS setup
// records. Both remain in the existing interactive admission/physical class.
// Queue selection is serialized; network and durable IO never hold this lock.
func (p *Pump) tryFastJob() (*Job, bool) {
	p.fastSelectMu.Lock()
	defer p.fastSelectMu.Unlock()
	if p.tinyStreak >= dataTinyBurst {
		select {
		case j := <-p.dataFast:
			p.tinyStreak = 0
			return j, true
		default:
		}
	}
	select {
	case j := <-p.dataTiny:
		p.tinyStreak++
		return j, true
	default:
	}
	select {
	case j := <-p.dataFast:
		p.tinyStreak = 0
		return j, true
	default:
		return nil, false
	}
}

func (p *Pump) tookFastJob(tiny bool) {
	p.fastSelectMu.Lock()
	defer p.fastSelectMu.Unlock()
	if tiny {
		p.tinyStreak++
	} else {
		p.tinyStreak = 0
	}
}

func (p *Pump) nextDataJob(fastStreak *int) (*Job, bool) {
	if *fastStreak >= dataFastBurst {
		select {
		case j := <-p.dataBulk:
			*fastStreak = 0
			return j, true
		default:
			*fastStreak = 0
		}
	}
	if *fastStreak < dataFastBurst {
		if j, ok := p.tryFastJob(); ok {
			*fastStreak = *fastStreak + 1
			return j, true
		}
	}
	select {
	case <-p.ctx.Done():
		return nil, false
	case j := <-p.dataFast:
		p.tookFastJob(false)
		*fastStreak = *fastStreak + 1
		return j, true
	case j := <-p.dataTiny:
		p.tookFastJob(true)
		*fastStreak = *fastStreak + 1
		return j, true
	case j := <-p.dataBulk:
		*fastStreak = 0
		return j, true
	}
}

func (p *Pump) signalFastData() {
	if p.dataFastWake == nil {
		return
	}
	select {
	case p.dataFastWake <- struct{}{}:
	default:
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// Already queued rows drain immediately; the oldest row's residence bounds
// waiting for future rows, even after the preceding APPEND blocked a worker.
func batchResidence(first *Job, cap time.Duration) time.Duration {
	return max(time.Duration(0), cap-first.QueueWait(time.Now()))
}

func (p *Pump) collectDataBatch() ([]*Job, bool) {
	p.batchMu.Lock()
	defer p.batchMu.Unlock()

	// Fast/bulk fairness belongs to the serialized collector, not to an
	// individual physical writer. Writers rotate while APPENDs are in flight; a
	// per-worker streak would therefore reset in practice and could starve bulk
	// whenever the short-record queue stays busy.
	first, ok := p.nextDataJob(&p.batchFastStreak)
	if !ok {
		return nil, false
	}
	fastBatch := fastDataJob(first)
	maxRecords, targetBytes, delay := dataBatchMaxRecords, uint64(dataBatchTargetBytes), dataBatchDelay
	if !fastBatch {
		if policy, ok := p.link.(interface {
			BulkBatchPolicy() (uint64, time.Duration)
		}); ok {
			bytes, residence := policy.BulkBatchPolicy()
			targetBytes = min(uint64(dataBatchTargetBytes), max(uint64(32<<10), bytes))
			delay = min(dataBatchDelay, max(time.Duration(0), residence))
		}
	}
	if fastBatch {
		maxRecords, targetBytes, delay = dataFastMaxRecords, uint64(dataFastTargetBytes), dataFastDelay
	}
	batch := []*Job{first}
	payload := first.Attempt().PayloadBytes
	timer := time.NewTimer(batchResidence(first, delay))
	defer stopTimer(timer)
	// Socket reads may themselves coalesce for up to 5 ms. Bulk therefore gets
	// enough wall time to collect eleven 32 KiB records, but the actor signals
	// dataFastWake on every short record so this longer bulk deadline cannot
	// become head-of-line latency for an interactive exchange.
collect:
	for len(batch) < maxRecords && payload < targetBytes {
		if !fastBatch && !p.fastReserve && len(p.dataFast)+len(p.dataTiny) > 0 {
			// Without a dedicated fast collector the ordinary worker must yield
			// immediately. With a reserve, keep filling this bulk batch: the short
			// record is dequeued independently and fast physical waiters retain
			// priority over newly scheduled bulk APPENDs.
			break collect
		}
		if fastBatch {
			// The coalescing deadline bounds how long we wait for new fast DATA.
			// It must not fragment records that are already queued: with a 250 us
			// deadline the timer can be ready before this loop is scheduled, and a
			// random select choice would otherwise cut a full burst down to one row.
			if j, ok := p.tryFastJob(); ok {
				if j != nil {
					batch = append(batch, j)
					payload += j.Attempt().PayloadBytes
					p.batchFastStreak++
				}
				continue
			}
			select {
			case <-p.ctx.Done():
				for _, j := range batch {
					clear(j.frame.Payload)
				}
				return nil, false
			case j := <-p.dataFast:
				p.tookFastJob(false)
				if j != nil {
					batch = append(batch, j)
					payload += j.Attempt().PayloadBytes
					p.batchFastStreak++
				}
			case j := <-p.dataTiny:
				p.tookFastJob(true)
				if j != nil {
					batch = append(batch, j)
					payload += j.Attempt().PayloadBytes
					p.batchFastStreak++
				}
			case <-timer.C:
				break collect
			}
			continue
		}
		// As with the fast collector, an expired coalescing deadline limits
		// waiting for future records; it must not randomly beat bulk records that
		// are already queued. Consume ready backlog before entering the timed select.
		select {
		case j := <-p.dataBulk:
			if j != nil {
				batch = append(batch, j)
				payload += j.Attempt().PayloadBytes
			}
			continue
		default:
		}
		select {
		case <-p.ctx.Done():
			for _, j := range batch {
				clear(j.frame.Payload)
			}
			return nil, false
		case j := <-p.dataBulk:
			if j != nil {
				batch = append(batch, j)
				payload += j.Attempt().PayloadBytes
			}
		case <-p.dataFastWake:
			if !p.fastReserve && len(p.dataFast)+len(p.dataTiny) > 0 {
				break collect
			}
		case <-timer.C:
			break collect
		}
	}
	return batch, true
}

// collectFastBatch is used only by reserved fast workers. It deliberately does
// not take batchMu: ordinary collectors may be blocked in a bulk WriteBatch,
// and the whole point of the reserve is that short DATA must still be dequeued.
// Channel ownership keeps each Job exact-once even when an ordinary collector
// happens to become available at the same time.
func (p *Pump) collectFastBatch() ([]*Job, bool) {
	first, ready := p.tryFastJob()
	if !ready {
		select {
		case <-p.ctx.Done():
			return nil, false
		case first = <-p.dataFast:
			p.tookFastJob(false)
		case first = <-p.dataTiny:
			p.tookFastJob(true)
		}
	}
	if first == nil {
		return nil, false
	}
	batch := []*Job{first}
	payload := first.Attempt().PayloadBytes
	timer := time.NewTimer(batchResidence(first, dataFastDelay))
	defer stopTimer(timer)
	for len(batch) < dataFastMaxRecords && payload < uint64(dataFastTargetBytes) {
		// Prefer work already queued over an expired coalescing timer. The timer
		// limits waiting for a future row; it must not fragment a ready burst.
		if j, ok := p.tryFastJob(); ok {
			if j != nil {
				batch = append(batch, j)
				payload += j.Attempt().PayloadBytes
			}
			continue
		}
		select {
		case <-p.ctx.Done():
			for _, j := range batch {
				clear(j.frame.Payload)
			}
			return nil, false
		case j := <-p.dataFast:
			p.tookFastJob(false)
			if j != nil {
				batch = append(batch, j)
				payload += j.Attempt().PayloadBytes
			}
		case j := <-p.dataTiny:
			p.tookFastJob(true)
			if j != nil {
				batch = append(batch, j)
				payload += j.Attempt().PayloadBytes
			}
		case <-timer.C:
			return batch, true
		}
	}
	return batch, true
}

func (p *Pump) writeFastDataLoop(batchLink BatchLink) {
	for {
		batch, ok := p.collectFastBatch()
		if !ok {
			return
		}
		results := batchLink.WriteBatch(batch)
		if len(results) != len(batch) {
			p.emit(pumpEvent{err: ErrState})
			return
		}
		if !p.emit(pumpEvent{completions: results}) {
			return
		}
	}
}

func (p *Pump) writeDataLoop(batchLink BatchLink, batching bool) {
	fastStreak := 0
	for {
		if !batching {
			first, ok := p.nextDataJob(&fastStreak)
			if !ok {
				return
			}
			r := p.link.Write(first)
			if !p.emit(pumpEvent{completion: &r}) {
				return
			}
			continue
		}

		batch, ok := p.collectDataBatch()
		if !ok {
			return
		}
		// Queue collection is serialized, but the expensive durable bookkeeping
		// and IMAP APPEND below remain fully parallel across all writer workers.
		var release func()
		if admission, ok := p.link.(BulkPreparationLink); ok && !fastDataJob(batch[0]) {
			var err error
			release, err = admission.AcquireBulkPreparation(p.ctx)
			if err != nil {
				for _, job := range batch {
					clear(job.frame.Payload)
				}
				if p.ctx.Err() == nil {
					p.emit(pumpEvent{err: err})
				}
				return
			}
		}
		results := batchLink.WriteBatch(batch)
		if release != nil {
			release()
		}
		if len(results) != len(batch) {
			p.emit(pumpEvent{err: ErrState})
			return
		}
		if !p.emit(pumpEvent{completions: results}) {
			return
		}
	}
}

func (p *Pump) adopt(id uint64, socket Socket, deferInitial bool) (wire.Frame, error) {
	if len(p.sockets) >= p.cfg.Engine.Limits.Streams || (!deferInitial && len(p.controls) >= cap(p.controls)) {
		return wire.Frame{}, flow.ErrCredit
	}
	credit, err := p.engine.Activate(p.cfg.Engine.Generation, id)
	if err != nil {
		return wire.Frame{}, err
	}
	s := &socketState{socket: socket, writes: make(chan socketWrite, 1), done: make(chan struct{})}
	p.sockets[id] = s
	// Normal Add preserves the original asynchronous Pump control path. OPEN
	// callers may defer exactly the initial WINDOW so they can explicitly queue
	// OPEN_OK first and WINDOW second on the same lane.
	if !deferInitial {
		if err = p.control(credit); err != nil {
			return wire.Frame{}, err
		}
	}
	p.worker(func() { p.readSocket(id, socket) })
	p.worker(func() { p.writeSocket(id, s) })
	if deferInitial {
		return credit, nil
	}
	return wire.Frame{}, nil
}
func (p *Pump) readSocket(id uint64, s Socket) {
	for {
		b := make([]byte, wire.MaxData)
		n, err := s.Read(b)
		if n > 0 && n < len(b) && err == nil && p.cfg.ReadCoalesce > 0 {
			n, err = coalesceRead(s, b, n, p.cfg.ReadCoalesce)
		}
		if n < 0 || n > len(b) {
			p.emit(pumpEvent{err: io.ErrUnexpectedEOF})
			return
		}
		if n == 0 && err == nil {
			p.emit(pumpEvent{err: io.ErrNoProgress})
			return
		}
		r := &readResult{bulk: n > InteractiveDataThreshold, id: id, data: b[:n], err: err, ack: make(chan struct{})}
		if !p.emit(pumpEvent{read: r}) {
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case <-r.ack:
		}
		if err != nil {
			return
		}
	}
}
func (p *Pump) writeSocket(id uint64, s *socketState) {
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-s.done:
			return
		case work := <-s.writes:
			if p.ctx.Err() != nil {
				return
			}
			_ = s.socket.SetWriteDeadline(time.Now().Add(30 * time.Second))
			result := writeResult{id: id, half: work.half}
			if work.half {
				result.err = s.socket.CloseWrite()
			} else {
				result.n, result.err = s.socket.Write(work.data)
			}
			if !p.emit(pumpEvent{write: &result}) {
				return
			}
			if result.err != nil {
				return
			}
		}
	}
}
func (p *Pump) handle(event pumpEvent) error {
	if event.err != nil {
		return event.err
	}
	g := p.cfg.Engine.Generation
	if input := event.input; input != nil {
		if input.Generation != g {
			return ErrGeneration
		}
		if (input.Frame == nil) == (input.Cleaned == 0) {
			return ErrState
		}
		if input.Cleaned != 0 {
			return p.engine.Cleaned(g, input.Cleaned)
		}
		before := p.engine.AcknowledgedBytes()
		out, err := p.engine.Receive(g, *input.Frame)
		if delta := p.engine.AcknowledgedBytes() - before; delta > 0 {
			if p.cfg.Acknowledged != nil {
				p.cfg.Acknowledged(delta)
			}
			if p.cfg.DeliveryProgress != nil {
				ctrl, decodeErr := wire.DecodeControl(*input.Frame)
				if decodeErr == nil {
					switch m := ctrl.(type) {
					case wire.Received:
						p.cfg.DeliveryProgress(input.Frame.Stream, m.Frontier)
					case wire.Progress:
						p.cfg.DeliveryProgress(input.Frame.Stream, m.Receipt)
					}
				}
			}
		}
		if err != nil {
			return err
		}
		if p.engine.Aborted(input.Frame.Stream) {
			p.dropSocket(input.Frame.Stream)
		}
		return p.controlAll(out)
	}
	if result := event.completion; result != nil {
		return p.completeOne(g, *result)
	}
	if results := event.completions; results != nil {
		var batchErr error
		for _, result := range results {
			if result.Generation != g {
				return ErrGeneration
			}
			if err := p.completeAttempt(g, result); err != nil {
				return err
			}
			if result.Usage != nil {
				return ErrState // Batch writers publish one physical report themselves.
			}
			if result.NonFatal {
				if result.Outcome != flow.CommitUnknown && result.Outcome != flow.NotCommitted {
					return ErrState
				}
				continue
			}
			if batchErr == nil && result.Err != nil {
				batchErr = result.Err
			}
		}
		// A definitive batch failure still ends the owner after every logical
		// attempt has been completed. CommitUnknown with durable bookkeeping is
		// deliberately non-fatal: repair/NACK owns the retry decision and a failed
		// physical carrier must not cascade into the logical stream owner.
		return batchErr
	}
	if r := event.read; r != nil {
		s := p.sockets[r.id]
		if s == nil {
			clear(r.data)
			close(r.ack)
			return nil
		}
		if s.read != nil || s.localEOF {
			return ErrState
		}
		s.read = r
		return nil
	}
	if r := event.write; r != nil {
		s := p.sockets[r.id]
		if s == nil {
			return nil
		}
		if !s.writing {
			return ErrState
		}
		s.writing = false
		if r.half {
			if r.err == nil {
				s.halfClosed = true
			}
			if r.err != nil {
				return p.abortSocketFailure(r.id, "socket_half_close", r.err)
			}
			return nil
		}
		if r.n < 0 || r.n > s.writeLen {
			return io.ErrShortWrite
		}
		if r.n > 0 {
			credit, err := p.engine.Delivered(g, r.id, uint64(r.n))
			if err != nil {
				return err
			}
			if err = p.control(credit); err != nil {
				return err
			}
		}
		if r.err != nil {
			return p.abortSocketFailure(r.id, "socket_write", r.err)
		}
		if r.n == 0 {
			return io.ErrNoProgress
		}
		return nil
	}
	return ErrState
}
func (p *Pump) completeOne(g uint64, result Completion) error {
	if result.Generation != g {
		return ErrGeneration
	}
	if err := p.completeAttempt(g, result); err != nil {
		return err
	}
	if result.Usage != nil {
		f, err := wire.EncodeControl(0, *result.Usage)
		if err != nil {
			return err
		}
		if err = p.control(f); err != nil {
			return err
		}
	}
	if result.NonFatal {
		if result.Outcome != flow.CommitUnknown && result.Outcome != flow.NotCommitted {
			return ErrState
		}
		return nil
	}
	return result.Err
}

func (p *Pump) completeAttempt(g uint64, result Completion) error {
	var item uint64
	if result.RetryWake != nil {
		if !result.NonFatal || result.Outcome != flow.NotCommitted {
			return ErrState
		}
		// Derive ownership from the actor's handoff, never a worker-supplied item.
		for id, pending := range p.engine.pending {
			if pending.writingAttempt == result.Attempt {
				item = id
				break
			}
		}
	}
	if err := p.engine.Complete(g, result.Attempt, result.Outcome); err != nil {
		return err
	}
	if item != 0 {
		if p.retryWake == nil {
			p.retryWake = make(map[uint64]<-chan struct{})
		}
		p.retryWake[item] = result.RetryWake
	}
	return nil
}

func (p *Pump) progress() error {
	g := p.cfg.Engine.Generation
	ids := make([]uint64, 0, len(p.sockets))
	for id := range p.sockets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > 0 {
		p.turn = (p.turn + 1) % len(ids)
	}
	for index := range ids {
		id := ids[(index+p.turn)%len(ids)]
		s := p.sockets[id]
		if err := p.acceptRead(id, s); err != nil {
			return err
		}
		if p.sockets[id] == nil {
			continue
		}
		if !s.writing && !s.halfClosed {
			b, err := p.engine.Peek(g, id)
			if err != nil {
				return err
			}
			if len(b) > 0 {
				s.writing = true
				s.writeLen = len(b)
				s.writes <- socketWrite{data: b}
			} else if p.engine.HalfClosed(g, id) {
				s.writing = true
				s.writes <- socketWrite{half: true}
			}
		}
		if s.localEOF && s.halfClosed && !s.writing {
			err := p.engine.Retire(g, id)
			if err == nil {
				p.dropSocket(id)
			} else if !errors.Is(err, ErrState) {
				return err
			}
		}
	}
	pending := p.engine.PendingFair(p.lastDataStream)
	exists := make(map[uint64]bool, len(pending))
	for _, id := range pending {
		exists[id] = true
	}
	for id := range p.attempted {
		if !exists[id] {
			delete(p.attempted, id)
			delete(p.retryWake, id)
		}
	}
	for _, id := range pending {
		if wake := p.retryWake[id]; wake != nil {
			select {
			case <-wake:
				delete(p.retryWake, id)
			default:
				continue
			}
		}
		if p.attempted[id] && !p.engine.RetryReady(id) {
			continue
		}
		metadata, payloadBytes, err := p.engine.FrameInfo(g, id)
		if err != nil {
			return err
		}
		fast := p.engine.ItemInteractive(id, payloadBytes)
		queue := p.dataBulk
		if fast {
			queue = p.dataFast
			if p.dataTiny != nil && payloadBytes <= dataTinyThreshold {
				queue = p.dataTiny
			}
		}
		if len(queue) == cap(queue) {
			// Bulk saturation cannot consume the reserved short-record queue.
			// Leave the item queued in Engine and reconsider it after a worker drains.
			continue
		}
		if !fast && p.engine.tx.AttemptCount() >= p.cfg.Engine.Limits.Attempts-p.cfg.InteractiveAttemptReserve {
			continue
		}
		frame, err := p.engine.Frame(g, id)
		if err != nil {
			return err
		}
		size, err := p.link.Size(frame)
		if size == 0 || size > 2<<20 {
			if err == nil {
				err = ErrState
			}
		}
		if err != nil {
			return err
		}
		job, err := p.engine.Begin(g, id, p.cfg.Account, size, time.Since(p.started))
		if err != nil {
			if errors.Is(err, flow.ErrCredit) {
				// One exhausted repair or account reservation cannot fence siblings.
				clear(frame.Payload)
				continue
			}
			return err
		}
		p.attempted[id] = true
		p.lastDataStream = metadata.Stream
		job.queuedAt = time.Now()
		queue <- job
		if fast {
			p.signalFastData()
		}
	}
	return nil
}

func (p *Pump) acceptRead(id uint64, s *socketState) error {
	r := s.read
	if r == nil {
		return nil
	}
	if len(r.data) > 0 {
		if p.engine.fragmentedAdmission(id, len(r.data)) {
			r.bulk = true
		}
		// TCP can split the end of a large read into an arbitrarily short fragment.
		// Keep its bulk admission class while that stream still has unreceipted
		// bytes; otherwise its tail can consume the short-stream credit reserve.
		// Once drained, a later short exchange on the same socket is interactive.
		if r.bulk {
			s.bulkRead = true
		} else if s.bulkRead {
			if p.engine.pendingStreamBytes(p.cfg.Engine.Generation, id) > 0 {
				r.bulk = true
			} else {
				s.bulkRead = false
			}
		}
		reserve := uint64(0)
		if r.bulk {
			reserve = p.cfg.InteractiveReserve
		}
		capacity := p.engine.AdmissionCapacityReserved(p.cfg.Engine.Generation, id, reserve)
		if r.bulk && p.cfg.BulkAdmissionLimit != nil {
			budget := min(p.cfg.BulkAdmissionLimit(), p.cfg.Engine.Limits.Logical)
			used := p.engine.tx.LogicalBytes()
			if used >= budget || p.engine.tx.ItemCount() >= p.cfg.Engine.Limits.Items-p.cfg.InteractiveItemReserve {
				capacity = 0
			} else {
				// Queue headroom is advisory: never split a full TCP record into
				// tiny fragments merely to fill an odd speculative-byte remainder.
				headroom := budget - used
				if headroom < uint64(len(r.data)) {
					capacity = 0
				} else {
					capacity = min(capacity, headroom)
				}
			}
			active := 0
			for _, socket := range p.sockets {
				if socket.bulkRead || socket.read != nil && socket.read.bulk {
					active++
				}
			}
			if active > 1 {
				share := max(uint64(2*wire.MaxData), 2*budget/uint64(active))
				pending := p.engine.pendingStreamBytes(p.cfg.Engine.Generation, id)
				if pending >= share {
					capacity = 0
				} else {
					headroom := share - pending
					if headroom < uint64(len(r.data)) {
						capacity = 0
					} else {
						capacity = min(capacity, headroom)
					}
				}
			}
		}
		n := min(len(r.data), int(capacity))
		if n == 0 {
			return nil
		}
		if _, err := p.engine.Queue(p.cfg.Engine.Generation, id, r.data[:n]); err != nil {
			return err
		}
		r.data = r.data[n:]
		if len(r.data) > 0 {
			return nil
		}
	}
	s.read = nil
	close(r.ack)
	if r.err != nil {
		if !errors.Is(r.err, io.EOF) {
			return p.abortSocketFailure(id, "socket_read", r.err)
		}
		s.localEOF = true
		fin, err := p.engine.Finish(p.cfg.Engine.Generation, id)
		if err != nil {
			return err
		}
		return p.control(fin)
	}
	return nil
}

func (p *Pump) dropSocket(id uint64) {
	if p.cfg.DeliveryStopped != nil {
		p.cfg.DeliveryStopped(id)
	}
	s := p.sockets[id]
	if s != nil {
		s.socket.Close()
		close(s.done)
		if s.read != nil {
			clear(s.read.data)
			close(s.read.ack)
			s.read = nil
		}
		delete(p.sockets, id)
	}
	if a := p.engine.aborts[id]; a != nil && !a.done {
		// Keep the logical route until peer RESET/ACK supplies the remote final.
		// Retiring it here discarded every late ACK and leaked stream admission.
		if p.awaitingRetirement == nil {
			p.awaitingRetirement = map[uint64]bool{}
		}
		p.awaitingRetirement[id] = true
		return
	}
	if s != nil || p.awaitingRetirement[id] {
		delete(p.awaitingRetirement, id)
		if p.cfg.Retired != nil {
			p.cfg.Retired(id)
		}
	}
}

func (p *Pump) abortSocketFailure(id uint64, operation string, err error) error {
	if p.cfg.SocketFailure != nil {
		p.cfg.SocketFailure(id, operation, err)
	}
	return p.abortSocket(id)
}

func (p *Pump) abortSocket(id uint64) error {
	f, e := p.engine.Abort(p.cfg.Engine.Generation, id, wire.ReasonClosed)
	if e != nil {
		return e
	}
	p.dropSocket(id)
	return p.control(f)
}

// Bound aggregation by wall time, never wait for a full block. This prevents a
// TCP/TLS fragment from becoming a separate IMAP APPEND for every socket Read.
func coalesceRead(s Socket, b []byte, n int, delay time.Duration) (int, error) {
	if err := s.SetReadDeadline(time.Now().Add(delay)); err != nil {
		// A half-closed pipe can reject deadline changes after a successful
		// final read. Deliver those bytes; the next Read determines EOF.
		return n, nil
	}
	defer s.SetReadDeadline(time.Time{})
	for n < len(b) {
		count, err := s.Read(b[n:])
		if count < 0 || count > len(b)-n {
			return n, io.ErrUnexpectedEOF
		}
		n += count
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return n, nil
			}
			return n, err
		}
		if count == 0 {
			return n, io.ErrNoProgress
		}
	}
	return n, nil
}
