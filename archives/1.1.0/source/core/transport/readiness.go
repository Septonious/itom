package transport

import "time"

// Ready reports admissible logical demand without draining a queue or touching
// encryption/grants. A worker samples WorkChanged before Ready/Prepare, so idle
// sessions need neither polling sleeps nor repeated grant transactions.
func (s *Session) Ready(plane Plane) bool { return s.ReadyFor(plane, AnyLedger) }
func (s *Session) ReadyFor(plane Plane, class LedgerClass) bool {
	if class > ControlLedger {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || plane > Bulk || len(s.dispatches) >= maxDispatches {
		return false
	}
	if len(s.dispatches) >= dataDispatchLimit {
		if plane == Bulk || class == DataLedger {
			return false
		}
		if class == AnyLedger {
			class = ControlLedger
		}
	}
	s.refillInteractiveLocked(time.Now())
	if plane == Fast && class != DataLedger && len(s.authorityOut) > 0 {
		return true
	}
	for _, r := range s.retries[plane] {
		f := s.flows[r.Stream]
		if class.accepts(r.Kind) && f != nil && (!f.stopped() || r.Kind == Reset || r.Kind == ResetAck) && (r.Kind != Data || !f.stopped() && r.Offset+uint64(len(r.Payload)) > f.receipt) {
			return true
		}
	}
	for _, f := range s.flows {
		if class != DataLedger && plane == Fast && (f.resetAckPending || f.localReset && !f.resetSent || !f.stopped() && (f.openPending || f.receiptPending || f.creditPending || f.finishAckPending || f.localFinish && f.tx.Len() == 0 && !f.finishSent)) {
			return true
		}
		if class == ControlLedger || f.stopped() || f.opening != openEstablished || f.tx.Len() == 0 || f.sent >= f.peerCredit || len(f.pending) >= 4096 || s.records >= 16384 {
			continue
		}
		short := s.shortLocked(f)
		if (plane == Fast) == short {
			return true
		}
	}
	return false
}

type SessionSnapshot struct {
	DecodedKinds                                   [12]uint64
	LastDecodedKindsAtMS                           [12]int64
	BackgroundAuthorityOutstanding                 int
	Streams, RetainedRecords, ActiveDispatches     int
	ActiveServices                                 int
	AuthorityOutstanding                           int
	AuthorityBuffered                              uint64
	SendBuffered, ReceiveBuffered, ReceiveReserved uint64
	Closed                                         bool
	FastPreparations                               int
	PreparationContention                          uint64
}

func (s *Session) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionSnapshot{DecodedKinds: s.decodedKinds, LastDecodedKindsAtMS: s.lastDecodedKindsAtMS, Streams: len(s.flows), RetainedRecords: s.records, ActiveDispatches: len(s.dispatches), ActiveServices: s.services, AuthorityOutstanding: s.authorityCount, BackgroundAuthorityOutstanding: len(s.authorityBackground), AuthorityBuffered: s.authorityBytes, SendBuffered: s.buffered, ReceiveBuffered: s.received, ReceiveReserved: s.reserved, Closed: s.closed, FastPreparations: s.preparingFast[DataLedger] + s.preparingFast[ControlLedger], PreparationContention: s.preparationContention}
}

// FlowSnapshot separates application queues, receiver-backed credit and
// publication state. It contains no target, payload, credentials or key data.
type FlowSnapshot struct {
	OpenPending, OpenSent                                                                    bool
	Opening                                                                                  uint8
	ID, Sent, Received, Consumed, PeerCredit, PeerReceipt, ReceiveLimit, SendBuffered        uint64
	Queued, Readable, Reordered, PendingRecords, Dispatches                                  int
	ReceiptPending, CreditPending, LocalFinish, FinishSent, FinishAcked, RemoteFinish, Reset bool
}

func (s *Session) FlowSnapshot(id uint64) (FlowSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil {
		return FlowSnapshot{}, false
	}
	return s.flowSnapshotLocked(f), true
}

func (s *Session) flowSnapshotLocked(f *stream) FlowSnapshot {
	snapshot := FlowSnapshot{OpenPending: f.openPending, OpenSent: f.openSent, Opening: uint8(f.opening), ID: f.id, Sent: f.sent, Received: f.recv, Consumed: f.consumed, PeerCredit: f.peerCredit, PeerReceipt: f.receipt, ReceiveLimit: f.receiveLimit(), SendBuffered: f.buffered, Queued: f.tx.Len(), Readable: f.rx.Len(), Reordered: len(f.reorder), PendingRecords: len(f.pending), ReceiptPending: f.receiptPending, CreditPending: f.creditPending, LocalFinish: f.localFinish, FinishSent: f.finishSent, FinishAcked: f.finishAcked, RemoteFinish: f.remoteFinish, Reset: f.stopped()}
	for _, d := range s.dispatches {
		if d.record.Stream == f.id {
			snapshot.Dispatches++
		}
	}
	return snapshot
}

// FlowSnapshots follows the bounded stream table order and exposes only queue,
// credit and progress state. Targets and application bytes are never included.
func (s *Session) FlowSnapshots() []FlowSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]FlowSnapshot, 0, len(s.flows))
	for _, id := range s.order {
		if f := s.flows[id]; f != nil {
			result = append(result, s.flowSnapshotLocked(f))
		}
	}
	return result
}
