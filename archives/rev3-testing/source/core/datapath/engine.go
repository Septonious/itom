// Package datapath joins byte accounting and recovery into one session owner.
// Engine methods are serialized by the owner. Disk/network/socket work runs
// outside it and returns generation-tagged completions; no method here does I/O.
package datapath

import (
	"context"
	"errors"
	"itom.local/core/flow"
	"itom.local/core/recovery"
	"itom.local/core/session"
	"itom.local/core/wire"
	"sort"
	"sync/atomic"
	"time"
)

var ErrGeneration = errors.New("DATA_GENERATION")
var ErrState = errors.New("DATA_STATE")

type Config struct {
	Generation                 uint64
	Limits                     flow.Limits
	AccountCaps                map[string]uint64
	ReceiveBytes, StreamWindow uint64
	MaxStreamWindow            uint64
	Metadata                   int
	Deadline, ProbeInterval    time.Duration
	DeliveryGrace              time.Duration
	IsolateStreamTimeouts      bool
	// Combine receive credit and receipt in an ITH9-compatible STATUS snapshot.
	CombinedReceiveProgress bool
}

func DefaultConfig(generation uint64, accounts []string) Config {
	caps := map[string]uint64{}
	for _, a := range accounts {
		caps[a] = 2 << 20
	}
	return Config{Generation: generation, Limits: flow.DefaultLimits(), AccountCaps: caps, ReceiveBytes: 12 << 20, StreamWindow: 256 << 10, Metadata: 4096, Deadline: 120 * time.Second, ProbeInterval: 2 * time.Second}
}

const terminalSendBudget = 8

type abortState struct {
	sends          int
	local, peer    uint64
	code           wire.Reason
	done           bool
	quiet          bool
	next, deadline time.Duration
}

type stream struct {
	receiveWindow, receivedSinceGrowth uint64
	peerWindowSeen, creditStalled      bool
	next                               uint64
	burstBytes                         uint64
	fragmentedBytes                    uint64
	fragmentedBulk                     bool
	fragmentedAt                       time.Duration
	bulk                               bool
	localFin, peerFin                  *uint64
	finAck                             bool
	nackID                             uint64
	finStarted                         time.Duration
}
type pending struct {
	stream         uint64
	watch          *recovery.Watch
	repair         *recovery.Repair
	requested      bool
	rangeWanted    wire.ByteRange
	repairAttempt  uint64
	writingAttempt uint64
	repairAfter    time.Duration
}
type Engine struct {
	acknowledgedBytes uint64
	cfg               Config
	tx                *flow.Tx
	rx                *flow.Rx
	registry          *session.Registry
	streams           map[uint64]*stream
	closed            map[uint64]uint64
	aborts            map[uint64]*abortState
	pending           map[uint64]*pending
	ctx               context.Context
	cancel            context.CancelFunc
	stopped           bool
	now               time.Duration
	nextPoll          time.Duration
	nextUnknownProbe  time.Duration
}

func New(parent context.Context, c Config) (*Engine, error) {
	if c.MaxStreamWindow == 0 {
		c.MaxStreamWindow = c.StreamWindow
	}
	if c.Generation == 0 || c.StreamWindow == 0 || c.StreamWindow > flow.MaxStreamWindow || c.MaxStreamWindow < c.StreamWindow || c.MaxStreamWindow > flow.MaxStreamWindow || c.Deadline <= 0 || c.ProbeInterval <= 0 || c.DeliveryGrace < 0 || c.DeliveryGrace > c.ProbeInterval {
		return nil, ErrState
	}
	tx, err := flow.NewTx(c.Limits, c.AccountCaps)
	if err != nil {
		return nil, err
	}
	rx, err := flow.NewRx(c.ReceiveBytes, c.Limits.Streams, c.Limits.Items)
	if err != nil {
		return nil, err
	}
	reg, err := session.NewRegistry(c.Limits.Streams, c.Metadata)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	return &Engine{cfg: c, tx: tx, rx: rx, registry: reg, streams: map[uint64]*stream{}, closed: map[uint64]uint64{}, aborts: map[uint64]*abortState{}, pending: map[uint64]*pending{}, ctx: ctx, cancel: cancel}, nil
}
func (e *Engine) check(g uint64) error {
	if g != e.cfg.Generation || e.stopped || e.ctx.Err() != nil {
		return ErrGeneration
	}
	return nil
}
func (e *Engine) clock(now time.Duration) error {
	if now < e.now || now < 0 {
		return ErrState
	}
	e.now = now
	return nil
}

// Activate follows authenticated OPEN_OK / successful adopted TCP dial only.
func (e *Engine) Activate(g, id uint64) (wire.Frame, error) {
	if err := e.check(g); err != nil {
		return wire.Frame{}, err
	}
	if len(e.streams)+len(e.closed) >= e.cfg.Metadata {
		return wire.Frame{}, session.ErrCapacity
	}
	if _, ok := e.streams[id]; ok {
		return wire.Frame{}, ErrState
	}
	if _, err := e.registry.Open(id); err != nil {
		return wire.Frame{}, err
	}
	if err := e.tx.OpenStream(id); err != nil {
		return wire.Frame{}, err
	}
	if err := e.rx.OpenStream(id, e.cfg.StreamWindow); err != nil {
		return wire.Frame{}, err
	}
	e.streams[id] = &stream{receiveWindow: e.cfg.StreamWindow}
	return e.credit(id)
}
func (e *Engine) credit(id uint64) (wire.Frame, error) {
	_, _, limit, err := e.rx.Frontier(id)
	if err != nil {
		return wire.Frame{}, err
	}
	return wire.EncodeControl(id, wire.Credit{MaxStream: limit, MaxData: e.rx.Snapshot().MaxData})
}
func (e *Engine) Queue(g, id uint64, b []byte) (uint64, error) {
	if err := e.check(g); err != nil {
		return 0, err
	}
	s := e.streams[id]
	if s == nil || s.localFin != nil {
		return 0, ErrState
	}
	item, err := e.tx.Admit(id, b)
	if err != nil {
		if errors.Is(err, flow.ErrCredit) {
			s.creditStalled = true
		}
		return 0, err
	}
	s.creditStalled = false
	s.next += uint64(len(b))
	s.burstBytes += uint64(len(b))
	if len(b) > InteractiveDataThreshold || e.now-s.fragmentedAt > 2*time.Second {
		s.fragmentedBytes, s.fragmentedBulk = 0, false
	}
	if len(b) <= InteractiveDataThreshold {
		s.fragmentedBytes = min(uint64(64<<10), s.fragmentedBytes+uint64(len(b)))
		if s.fragmentedBytes >= 64<<10 {
			s.fragmentedBulk = true
		}
	}
	s.fragmentedAt = e.now
	if len(b) > InteractiveDataThreshold || s.burstBytes >= 64<<10 {
		s.bulk = true
	}
	if len(b) > dataTinyThreshold && s.fragmentedBulk {
		s.bulk = true
	}
	e.pending[item] = &pending{stream: id, repair: recovery.NewRepair()}
	return item, nil
}
func (e *Engine) FrameInfo(g, item uint64) (wire.Frame, uint64, error) {
	if err := e.check(g); err != nil {
		return wire.Frame{}, 0, err
	}
	return e.tx.FrameInfo(item)
}

// ItemInteractive makes queue selection use the same owner state as Begin.
func (e *Engine) ItemInteractive(item uint64, payloadBytes uint64) bool {
	p := e.pending[item]
	return p != nil && e.streams[p.stream] != nil && !e.streams[p.stream].bulk && payloadBytes <= InteractiveDataThreshold
}

func (e *Engine) acknowledgeClass(s *stream, frontier uint64) {
	if frontier == s.next {
		s.bulk = false
		s.burstBytes = 0
	}
}

// Preserve short-stream admission credit when sustained small TCP reads are
// drained between fragments. Classification never changes charged byte debt.
func (e *Engine) fragmentedAdmission(id uint64, payload int) bool {
	s := e.streams[id]
	return s != nil && payload > dataTinyThreshold && s.fragmentedBulk && e.now-s.fragmentedAt <= 2*time.Second
}

func (e *Engine) Frame(g, item uint64) (wire.Frame, error) {
	if err := e.check(g); err != nil {
		return wire.Frame{}, err
	}
	return e.tx.Frame(item)
}

// Begin schedules one physical worker job. It is NOT permission to APPEND;
// Writer must durably charge the matching storage grant first.
func (e *Engine) Begin(g, item uint64, account string, encoded uint64, now time.Duration) (*Job, error) {
	if err := e.check(g); err != nil {
		return nil, err
	}
	if err := e.clock(now); err != nil {
		return nil, err
	}
	if now > time.Duration(1<<63-1)-e.cfg.Deadline {
		return nil, ErrState
	}
	p := e.pending[item]
	if p == nil {
		return nil, ErrState
	}
	f, err := e.tx.Frame(item)
	if err != nil {
		return nil, err
	}
	var a flow.Attempt
	if p.requested {
		a, err = e.tx.BeginRepair(item, account, encoded, p.rangeWanted.Start, p.rangeWanted.End)
	} else {
		if err = e.tx.Reserve(item, account); err != nil {
			return nil, err
		}
		a, err = e.tx.Begin(item, encoded)
		if err != nil {
			_ = e.tx.CancelReservation(item)
		}
	}
	if err != nil {
		return nil, err
	}
	if p.watch == nil {
		if now > time.Duration(1<<63-1)-e.cfg.Deadline {
			return nil, ErrState
		}
		p.watch, err = recovery.New(now, now+e.cfg.Deadline, e.cfg.ProbeInterval, 64)
		if err != nil {
			return nil, err
		}
	}
	if p.requested {
		p.repairAttempt = a.ID
	}
	p.requested = false
	p.writingAttempt = a.ID
	return &Job{generation: g, attempt: a, frame: f, ctx: e.ctx, bulk: e.streams[p.stream].bulk, claimed: new(atomic.Bool)}, nil
}
func (e *Engine) Complete(g, attempt uint64, outcome flow.Outcome) error {
	if err := e.check(g); err != nil {
		return err
	}
	if err := e.tx.Complete(attempt, outcome); err != nil {
		return err
	}
	// An ambiguous physical writer already knows that waiting for the next
	// periodic status can add a full two-second rejoin stall. Advance one probe
	// per configured interval; only an authenticated NACK authorizes DATA repair.
	// Neither the outcome nor any logical/physical debt is changed here.
	if outcome == flow.CommitUnknown && e.now >= e.nextUnknownProbe && e.now <= time.Duration(1<<63-1)-e.cfg.ProbeInterval {
		e.nextUnknownProbe = e.now + e.cfg.ProbeInterval
		if e.nextPoll > e.now {
			e.nextPoll = e.now
		}
	}
	// Only the matching physical outcome ends writer ownership. Peer STATUS
	// can overtake a queued/grant-blocked original APPEND, so original attempts
	// need this guard just as repairs do. A successful APPEND gets a bounded
	// BODY-visibility grace; uncertain APPEND remains immediately repairable.
	for _, p := range e.pending {
		if p.writingAttempt == attempt {
			p.writingAttempt = 0
			p.repairAfter = e.now
			if outcome == flow.Committed && e.now <= time.Duration(1<<63-1)-e.cfg.DeliveryGrace {
				p.repairAfter += e.cfg.DeliveryGrace
			}
		}
		if p.repairAttempt == attempt {
			if outcome == flow.NotCommitted {
				// Retain the same authenticated NACK's unspent permit. A visibility,
				// flight or wallet deferral is scheduling, not another physical retry.
				// RetryWake still parks it until the admission predicate changes.
				p.requested = true
			} else {
				p.repair.Complete()
			}
			p.repairAttempt = 0
		}
	}
	return nil
}

// Cleaned follows locally verified physical cleanup, never a receipt.
func (e *Engine) Cleaned(g, attempt uint64) error {
	if err := e.check(g); err != nil {
		return err
	}
	return e.tx.Cleanup(attempt)
}
func (e *Engine) prune() {
	for id, p := range e.pending {
		if _, _, err := e.tx.FrameInfo(id); err != nil {
			if p.watch != nil {
				p.watch.Receipt()
			}
			delete(e.pending, id)
		}
	}
}

// receiveProgress publishes only this receiver's cumulative state. Attempted=0
// is a conservative lower bound, so this feedback cannot trigger speculative
// NACKs when another carrier's DATA is still in flight. Periodic status/probes
// continue to advertise the real attempted frontier and terminal state.
func (e *Engine) receiveProgress(id uint64) (wire.Frame, error) {
	receipt, _, limit, err := e.rx.Frontier(id)
	if err != nil {
		return wire.Frame{}, err
	}
	return wire.EncodeControl(id, wire.Progress{Receipt: receipt, MaxStream: limit, MaxData: e.rx.Snapshot().MaxData})
}

func (e *Engine) status(id uint64) (wire.Frame, error) {
	s := e.streams[id]
	if s == nil {
		return wire.Frame{}, ErrState
	}
	attempted, err := e.tx.AttemptedEnd(id)
	if err != nil {
		return wire.Frame{}, err
	}
	receipt, _, limit, err := e.rx.Frontier(id)
	if err != nil {
		return wire.Frame{}, err
	}
	p := wire.Progress{Attempted: attempted, Receipt: receipt, MaxStream: limit, MaxData: e.rx.Snapshot().MaxData}
	if s.localFin != nil {
		p.HasFinal = true
		p.Final = *s.localFin
	}
	return wire.EncodeControl(id, p)
}

// Receive takes only authenticated frames for this session generation.
// Output controls need the independent control reserve, not a DATA permit.
func (e *Engine) Receive(g uint64, f wire.Frame) ([]wire.Frame, error) {
	if err := e.check(g); err != nil {
		return nil, err
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if f.Type == wire.Reset || f.Type == wire.ResetAck {
		c, err := wire.DecodeControl(f)
		if err != nil {
			return nil, err
		}
		m := c.(wire.Abort)
		a := e.aborts[f.Stream]
		if a == nil {
			if m.Ack {
				return nil, ErrState
			}
			if _, err = e.Abort(g, f.Stream, m.Code); err != nil {
				return nil, err
			}
			a = e.aborts[f.Stream]
		}
		if !a.done {
			if err = e.rx.Reset(f.Stream, m.Final); err != nil {
				return nil, err
			}
			if err = e.registry.Close(f.Stream); err != nil {
				return nil, err
			}
			a.peer = m.Final
			a.done = true
			e.closed[f.Stream] = m.Final
			delete(e.streams, f.Stream)
		} else if a.peer != m.Final {
			return nil, ErrState
		}
		if m.Ack {
			return nil, nil
		}
		ack, _ := wire.EncodeControl(f.Stream, wire.Abort{Ack: true, Final: a.local, Code: a.code})
		return []wire.Frame{ack}, nil
	}
	if a := e.aborts[f.Stream]; a != nil && !a.done {
		return nil, nil
	}
	s := e.streams[f.Stream]
	if s == nil {
		if final, closed := e.closed[f.Stream]; closed {
			if f.Type == wire.Fin {
				c, err := wire.DecodeControl(f)
				if err != nil {
					return nil, err
				}
				if c.(wire.Finish).Final != final {
					return nil, ErrState
				}
				ack, _ := wire.EncodeControl(f.Stream, wire.Finish{Ack: true, Final: final})
				return []wire.Frame{ack}, nil
			}
			return nil, nil
		}
		return nil, ErrState
	}
	out := []wire.Frame{}
	emit := func(c wire.Control) error {
		x, err := wire.EncodeControl(f.Stream, c)
		if err == nil {
			out = append(out, x)
		}
		return err
	}
	if f.Type == wire.Data {
		front, _, err := e.rx.Receive(f)
		if err != nil {
			return nil, err
		}
		if e.cfg.CombinedReceiveProgress {
			progress, x := e.receiveProgress(f.Stream)
			if x != nil {
				return nil, x
			}
			return []wire.Frame{progress}, nil
		}
		err = emit(wire.Received{Frontier: front})
		return out, err
	}
	ctrl, err := wire.DecodeControl(f)
	if err != nil {
		return nil, err
	}
	switch m := ctrl.(type) {
	case wire.Credit:
		err = e.tx.UpdateCredit(f.Stream, m)
		if err == nil {
			s.peerWindowSeen = true
		}
	case wire.Received:
		var released uint64
		released, err = e.tx.Receipt(f.Stream, m.Frontier)
		if err == nil {
			e.acknowledgedBytes += released
			e.acknowledgeClass(s, m.Frontier)
		}
		if err == nil {
			e.prune()
		}
	case wire.Probe:
		var x wire.Frame
		x, err = e.status(f.Stream)
		if err == nil {
			out = append(out, x)
		}
	case wire.Progress:
		// Reject impossible peer progress before mutating credit or receipts.
		var missing []wire.ByteRange
		missing, err = e.rx.Missing(f.Stream, m.Attempted, wire.MaxNackRanges)
		if err != nil {
			break
		}
		if m.HasFinal {
			err = e.rx.Fin(f.Stream, m.Final)
			if err != nil {
				break
			}
			v := m.Final
			s.peerFin = &v
		}
		var released uint64
		released, err = e.tx.Receipt(f.Stream, m.Receipt)
		if err == nil {
			e.acknowledgedBytes += released
			e.acknowledgeClass(s, m.Receipt)
		}
		if err != nil {
			break
		}
		e.prune()
		err = e.tx.UpdateCredit(f.Stream, wire.Credit{MaxStream: m.MaxStream, MaxData: m.MaxData})
		if err != nil {
			break
		}
		s.peerWindowSeen = true
		if len(missing) > 0 {
			if s.nackID == ^uint64(0) {
				err = ErrState
				break
			}
			s.nackID++
			err = emit(wire.Missing{ID: s.nackID, Ranges: missing})
		}
	case wire.Missing:
		for id, p := range e.pending {
			if p.stream != f.Stream || p.watch == nil || p.writingAttempt != 0 || e.now < p.repairAfter || e.tx.IsQueued(id) {
				continue
			}
			frame, payloadBytes, x := e.tx.FrameInfo(id)
			if x != nil {
				continue
			}
			end := frame.Offset + payloadBytes
			for _, r := range m.Ranges {
				start := max(r.Start, frame.Offset)
				stop := min(r.End, end)
				if start < stop && p.repair.Nack(m.ID) {
					p.requested = true
					p.rangeWanted = wire.ByteRange{Start: start, End: stop}
					break
				}
			}
		}
	case wire.Finish:
		if m.Ack {
			if s.localFin == nil || *s.localFin != m.Final {
				err = ErrState
			} else {
				s.finAck = true
			}
		} else {
			err = e.rx.Fin(f.Stream, m.Final)
			if err == nil {
				v := m.Final
				s.peerFin = &v
				err = emit(wire.Finish{Ack: true, Final: m.Final})
			}
		}
	default:
		err = ErrState
	}
	return out, err
}
func (e *Engine) Peek(g, id uint64) ([]byte, error) {
	if err := e.check(g); err != nil {
		return nil, err
	}
	return e.rx.Peek(id, wire.MaxData)
}

// Delivered advances by actual successful socket bytes, including n>0 with err.
func (e *Engine) Delivered(g, id, n uint64) (wire.Frame, error) {
	if err := e.check(g); err != nil {
		return wire.Frame{}, err
	}
	if err := e.rx.Delivered(id, n); err != nil {
		return wire.Frame{}, err
	}
	_, delivered, _, err := e.rx.Frontier(id)
	if err != nil {
		return wire.Frame{}, err
	}
	s := e.streams[id]
	s.receivedSinceGrowth += n
	if s.receiveWindow < e.cfg.MaxStreamWindow && s.receivedSinceGrowth >= s.receiveWindow/2 {
		s.receiveWindow = min(e.cfg.MaxStreamWindow, s.receiveWindow*2)
		s.receivedSinceGrowth = 0
	}
	if delivered > wire.MaxOffset-s.receiveWindow {
		return wire.Frame{}, ErrState
	}
	if err = e.rx.UpdateStreamLimit(id, delivered+s.receiveWindow); err != nil {
		return wire.Frame{}, err
	}
	if e.cfg.CombinedReceiveProgress {
		return e.receiveProgress(id)
	}
	return e.credit(id)
}
func (e *Engine) Finish(g, id uint64) (wire.Frame, error) {
	if err := e.check(g); err != nil {
		return wire.Frame{}, err
	}
	s := e.streams[id]
	if s == nil {
		return wire.Frame{}, ErrState
	}
	if s.localFin == nil {
		v := s.next
		s.localFin = &v
		s.finStarted = e.now
	}
	return wire.EncodeControl(id, wire.Finish{Final: *s.localFin})
}
func (e *Engine) HalfClosed(g, id uint64) bool { return e.check(g) == nil && e.rx.HalfClosed(id) }

// Poll probes lost DATA/receipt/window and repeats unacknowledged FIN. It never
// creates a DATA retry without an authenticated NACK. Runtime isolation resets
// only expired streams; the carrier watchdog owns loss of peer liveness.
func (e *Engine) Poll(g uint64, now time.Duration) ([]wire.Frame, error) {
	if err := e.check(g); err != nil {
		return nil, err
	}
	if err := e.clock(now); err != nil {
		return nil, err
	}
	expired := map[uint64]bool{}
	for _, p := range e.pending {
		if p.watch == nil {
			continue
		}
		a, err := p.watch.Poll(now)
		if err != nil {
			return nil, err
		}
		if a == recovery.DeadlineExpired {
			if !e.cfg.IsolateStreamTimeouts {
				return nil, context.DeadlineExceeded
			}
			expired[p.stream] = true
		}
	}
	for id, s := range e.streams {
		if e.aborts[id] == nil && s.localFin != nil && !s.finAck && now-s.finStarted >= e.cfg.Deadline {
			if !e.cfg.IsolateStreamTimeouts {
				return nil, context.DeadlineExceeded
			}
			expired[id] = true
		}
	}
	out := []wire.Frame{}
	for id := range expired {
		f, err := e.Abort(g, id, wire.ReasonTimeout)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if now < e.nextPoll {
		return out, nil
	}
	if now > time.Duration(1<<63-1)-e.cfg.ProbeInterval {
		return nil, ErrState
	}
	e.nextPoll = now + e.cfg.ProbeInterval
	needsStatus, needsPeerStatus := map[uint64]bool{}, map[uint64]bool{}
	for item, p := range e.pending {
		needsStatus[p.stream] = true
		if e.tx.IsQueued(item) {
			needsPeerStatus[p.stream] = true
		}
	}
	for id, s := range e.streams {
		if expired[id] {
			continue
		}
		if a := e.aborts[id]; a != nil {
			if !a.done {
				if now >= a.deadline {
					if !e.cfg.IsolateStreamTimeouts {
						return nil, context.DeadlineExceeded
					}
					a.quiet = true
				}
				if a.sends >= terminalSendBudget {
					a.quiet = true
				}
				if a.quiet || now < a.next {
					continue
				}
				a.sends++
				a.next = now + e.cfg.ProbeInterval
				f, _ := wire.EncodeControl(id, wire.Abort{Final: a.local, Code: a.code})
				out = append(out, f)
			}
			continue
		}
		// Only streams with unresolved local DATA/FIN need periodic recovery.
		// Idle streams have no repeated STATUS; carrier PING owns liveness.
		needsQuery := needsPeerStatus[id] || !s.peerWindowSeen || s.creditStalled
		needsRecovery := !s.peerWindowSeen || needsStatus[id] || s.localFin != nil && !s.finAck
		if !needsRecovery && !needsQuery {
			continue
		}
		// An idle receiver no longer sends periodic STATUS. Explicitly query it
		// while local queued DATA may be blocked by a lost initial/window credit.
		if needsQuery {
			probe, err := wire.EncodeControl(id, wire.Probe{})
			if err != nil {
				return nil, err
			}
			out = append(out, probe)
		}
		if !needsRecovery {
			continue
		}
		f, err := e.status(id)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		if s.localFin != nil && !s.finAck {
			f, _ := wire.EncodeControl(id, wire.Finish{Final: *s.localFin})
			out = append(out, f)
		}
	}
	return out, nil
}
func (e *Engine) Pending() []uint64            { return e.tx.PendingIDs() }
func (e *Engine) RetryReady(item uint64) bool  { return e.tx.IsQueued(item) || e.RepairReady(item) }
func (e *Engine) RepairReady(item uint64) bool { p := e.pending[item]; return p != nil && p.requested }
func (e *Engine) Snapshot() (flow.TxSnapshot, flow.RxSnapshot) {
	return e.tx.Snapshot(), e.rx.Snapshot()
}
func (e *Engine) Check() error {
	if err := e.tx.Check(); err != nil {
		return err
	}
	return e.rx.Check()
}
func (e *Engine) Stop() {
	if e.stopped {
		return
	}
	e.stopped = true
	e.cancel()
	for id := range e.streams {
		_ = e.tx.ForgetStream(id)
	}
	clear(e.pending)
	clear(e.streams)
	e.rx, _ = flow.NewRx(e.cfg.ReceiveBytes, e.cfg.Limits.Streams, e.cfg.Limits.Items)
}

// Retire only after both directions ended, local FIN was acknowledged, all
// locally sent bytes were receipted and all received bytes reached the socket.
// Compact final metadata replies to duplicate FIN without resurrecting a stream.
func (e *Engine) Retire(g, id uint64) error {
	if err := e.check(g); err != nil {
		return err
	}
	s := e.streams[id]
	if s == nil || s.localFin == nil || !s.finAck || s.peerFin == nil || !e.rx.HalfClosed(id) {
		return ErrState
	}
	for _, p := range e.pending {
		if p.stream == id {
			return ErrState
		}
	}
	if err := e.rx.Reset(id, *s.peerFin); err != nil {
		return err
	}
	if err := e.tx.ForgetStream(id); err != nil {
		return err
	}
	if err := e.registry.Close(id); err != nil {
		return err
	}
	e.closed[id] = *s.peerFin
	e.aborts[id] = &abortState{local: *s.localFin, peer: *s.peerFin, code: wire.ReasonClosed, done: true}
	delete(e.streams, id)
	return nil
}

// Abort releases logical buffers, but physical attempts remain charged until
// their journal confirms GC. The peer supplies its own final byte offset.
func (e *Engine) Abort(g, id uint64, code wire.Reason) (wire.Frame, error) {
	if err := e.check(g); err != nil {
		return wire.Frame{}, err
	}
	if a := e.aborts[id]; a != nil {
		return wire.EncodeControl(id, wire.Abort{Final: a.local, Code: a.code})
	}
	s := e.streams[id]
	if s == nil {
		return wire.Frame{}, ErrState
	}
	a := &abortState{sends: 1, local: s.next, code: code, next: e.now + e.cfg.ProbeInterval, deadline: e.now + e.cfg.Deadline}
	f, err := wire.EncodeControl(id, wire.Abort{Final: a.local, Code: code})
	if err != nil {
		return wire.Frame{}, err
	}
	if err = e.tx.ForgetStream(id); err != nil {
		return wire.Frame{}, err
	}
	for item, p := range e.pending {
		if p.stream == id {
			delete(e.pending, item)
		}
	}
	e.aborts[id] = a
	return f, nil
}
func (e *Engine) Aborted(id uint64) bool { return e.aborts[id] != nil }

// Rotate between streams while preserving item order within each stream.
// An interactive stream must not wait behind all queued items of a bulk stream.
func (e *Engine) PendingFair(after uint64) []uint64 {
	byStream := map[uint64][]uint64{}
	for _, id := range e.Pending() {
		stream := e.pending[id].stream
		byStream[stream] = append(byStream[stream], id)
	}
	streams := make([]uint64, 0, len(byStream))
	for id := range byStream {
		streams = append(streams, id)
	}
	sort.Slice(streams, func(i, j int) bool {
		a, b := streams[i], streams[j]
		if (a > after) != (b > after) {
			return a > after
		}
		return a < b
	})
	ids := make([]uint64, 0, len(e.pending))
	for round := 0; ; round++ {
		added := false
		for _, stream := range streams {
			if round < len(byStream[stream]) {
				ids = append(ids, byStream[stream][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return ids
}

func (e *Engine) AdmissionCapacity(g, id uint64) uint64 { return e.AdmissionCapacityReserved(g, id, 0) }
func (e *Engine) AdmissionCapacityReserved(g, id, reserve uint64) uint64 {
	if e.check(g) != nil {
		return 0
	}
	capacity := e.tx.AdmissionCapacityReserved(id, reserve)
	if s := e.streams[id]; s != nil {
		s.creditStalled = capacity == 0
	}
	return capacity
}

// AcknowledgedBytes excludes duplicate and invalid receipts, aborts, and GC.
func (e *Engine) AcknowledgedBytes() uint64 { return e.acknowledgedBytes }

func (e *Engine) pendingStreamBytes(g, id uint64) uint64 {
	if e.check(g) != nil {
		return 0
	}
	return e.tx.PendingStreamBytes(id)
}
