package transport

import "time"

type commandClass uint8

const (
	commandAppend commandClass = iota
	commandBody
	commandInventory
	commandNoop
	commandIdle
	commandFlags
	commandStore
	commandExpunge
	commandClasses
)

// TimingSummary contains local monotonic durations, never an inferred network
// RTT. Maxima are retained independently of the mean. Sampling has no disk I/O,
// dynamic label cardinality, provider strings, body bytes or secret material.
type TimingSummary struct {
	Count   uint64 `json:"count"`
	TotalNS uint64 `json:"total_ns"`
	MaxNS   uint64 `json:"max_ns"`
}

func (s *TimingSummary) observe(d time.Duration) {
	if d < 0 {
		return
	}
	n := uint64(d.Nanoseconds())
	s.Count++
	s.TotalNS += n
	s.MaxNS = max(s.MaxNS, n)
}

type CommandTiming struct {
	Started      uint64        `json:"started"`
	Completed    uint64        `json:"completed"`
	Refused      uint64        `json:"refused"`
	Failed       uint64        `json:"failed"`
	Elapsed      TimingSummary `json:"elapsed"`
	Continuation TimingSummary `json:"continuation"`
	LiteralWrite TimingSummary `json:"literal_write"`
}

type ExecutorSnapshot struct {
	PendingTags       int           `json:"pending_tags"`
	WriterWaiters     int           `json:"writer_waiters"`
	FetchWaiters      int           `json:"fetch_waiters"`
	FetchActive       bool          `json:"fetch_active"`
	LiteralActive     bool          `json:"literal_active"`
	IdleActive        bool          `json:"idle_active"`
	Closed            bool          `json:"closed"`
	UIDPlusSupported  bool          `json:"uidplus_supported"`
	KeywordsSupported bool          `json:"keywords_supported"`
	IdleSupported     bool          `json:"idle_supported"`
	Failure           string        `json:"failure,omitempty"`
	CapacityRefusals  uint64        `json:"capacity_refusals"`
	WriterWait        TimingSummary `json:"writer_wait"`
	Append            CommandTiming `json:"append"`
	Fetch             CommandTiming `json:"fetch"`
	Inventory         CommandTiming `json:"inventory"`
	Noop              CommandTiming `json:"noop"`
	Idle              CommandTiming `json:"idle"`
	Flags             CommandTiming `json:"flags"`
	Store             CommandTiming `json:"store"`
	Expunge           CommandTiming `json:"expunge"`
}

// Snapshot reads counters only. It neither issues a probe nor spends mailbox
// permission, and remains available after a failed or transferred connection.
func (e *Executor) Snapshot() ExecutorSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := ExecutorSnapshot{PendingTags: len(e.pending), WriterWaiters: e.writerWaiters, FetchWaiters: e.fetchWaiters,
		Closed: e.closed != nil, UIDPlusSupported: e.uidPlus, KeywordsSupported: e.keywords, IdleSupported: e.idleSupported, CapacityRefusals: e.capacityRefusals, WriterWait: e.writerWait,
		Append: e.timing[commandAppend], Fetch: e.timing[commandBody], Inventory: e.timing[commandInventory], Noop: e.timing[commandNoop], Idle: e.timing[commandIdle], Flags: e.timing[commandFlags], Store: e.timing[commandStore], Expunge: e.timing[commandExpunge]}
	if e.closed != nil {
		s.Failure = serviceFailure(e.closed)
	} else {
		s.FetchActive, s.LiteralActive, s.IdleActive = e.fetch != nil, e.literal != nil, e.idle != nil
	}
	return s
}

func (e *Executor) completedLocked(op *operation, failed bool) {
	m := &e.timing[op.class]
	m.Elapsed.observe(time.Since(op.started))
	if failed {
		m.Failed++
	} else {
		m.Completed++
		if op.result.Err != nil {
			m.Refused++
		}
	}
}
