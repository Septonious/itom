package tunnel

import (
	"context"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"time"
)

// A bounded actor snapshot correlates readiness, usable permits and physical
// debt without reading mutable grant/cleanup maps from a diagnostic goroutine.
type laneProgress struct {
	LatencyGovernor                                latencyGovernorSnapshot
	DataCommits, ControlCommits                    journal.CommitSnapshot
	Account                                        string
	User                                           string
	Server                                         bool
	Epoch                                          uint64
	Ready                                          bool
	Data, Control                                  pool.Amount
	LocalData, RemoteData                          pool.Amount
	FastUsable, BulkUsable, FastIssued, BulkIssued int
	RemoteCleanup, Outbox, ControlQueue            int
	OutboxRecords                                  int
	Known, Obsolete, ObsoleteControl, ObsoleteData int
	BulkControlBlocked                             bool
	Cleaning, CleanupWanted                        bool
	ProgressAgeMS                                  int64
	GroupCleaners, GroupCleanupWaiting             int
	MaintenanceIO                                  int64
	QueuedCleanup                                  bool
	Error                                          string
}

func (l *lane) captureProgress() laneProgress {
	s := laneProgress{Account: l.account.ID, User: l.user.ID, Server: l.side, Epoch: l.carrierEpoch, Ready: l.ready.Load(), FastUsable: len(l.fastGrants), BulkUsable: len(l.grants), RemoteCleanup: len(l.remoteGC), ControlQueue: len(l.urgentQueue) + len(l.controlQueue) + len(l.maintenanceQueue), Cleaning: l.gcBusy, CleanupWanted: l.cleanupWanted.Load(), ProgressAgeMS: l.remoteProgressAge(time.Now()).Milliseconds()}
	s.DataCommits, s.ControlCommits = l.data.CommitSnapshot(), l.control.CommitSnapshot()
	s.MaintenanceIO = l.maintenanceIO.Load()
	s.QueuedCleanup = l.cleanupQueued
	if l.stripe != nil {
		l.stripe.mu.Lock()
		s.LatencyGovernor = l.stripe.deliverySnapshotLocked()
		s.GroupCleaners = len(l.stripe.cleanupActive)
		s.GroupCleanupWaiting = len(l.stripe.cleanupQueue)
		l.stripe.mu.Unlock()
	}
	s.FastIssued, s.BulkIssued = l.issuedGrantCounts()
	var err error
	s.Data, err = l.data.Held(l.account.ID, l.user.ID)
	if err != nil {
		s.Error = err.Error()
	}
	s.LocalData, s.RemoteData, err = l.data.HeldByOrigin(l.account.ID, l.user.ID)
	if err != nil {
		s.Error = err.Error()
	}
	s.Control, err = l.control.Held(l.account.ID, l.user.ID)
	if err != nil {
		s.Error = err.Error()
	}
	l.mu.Lock()
	s.Outbox = len(l.out)
	s.Known = len(l.known)
	s.Obsolete = len(l.obsolete)
	for ref := range l.obsolete {
		if ref.Grant.Store == l.control.StoreID() {
			s.ObsoleteControl++
		}
		if ref.Grant.Store == l.data.StoreID() {
			s.ObsoleteData++
		}
	}
	for _, o := range l.out {
		s.OutboxRecords += o.records
	}
	s.BulkControlBlocked = l.bulkControlBlocked.Load()
	l.mu.Unlock()
	return s
}
func (l *lane) progressSnapshot(ctx context.Context) laneProgress {
	reply := make(chan laneProgress, 1)
	select {
	case l.progressRequests <- reply:
	case <-ctx.Done():
		return laneProgress{Account: l.account.ID, User: l.user.ID, Server: l.side, Error: ctx.Err().Error()}
	case <-l.ctx.Done():
		return laneProgress{Account: l.account.ID, User: l.user.ID, Server: l.side, Error: l.ctx.Err().Error()}
	}
	select {
	case s := <-reply:
		return s
	case <-ctx.Done():
		return laneProgress{Account: l.account.ID, User: l.user.ID, Server: l.side, Error: ctx.Err().Error()}
	case <-l.ctx.Done():
		return laneProgress{Account: l.account.ID, User: l.user.ID, Server: l.side, Error: l.ctx.Err().Error()}
	}
}
