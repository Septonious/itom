package transport

import (
	"context"
	"errors"
)

// Poll reads one predicted next UID after a completed scoped inventory, avoiding
// a separate UID enumeration on the contiguous new-message path. It
// never waits to collect a batch. UIDVALIDITY remains pinned, actual UID is read
// from every BODY, and a miss, ambiguous sequence movement, pressure or dependency forces
// inventory. A nonexistent UID is a legal empty query; no nonexistent sequence
// number is requested and no numeric UID gap is silently advanced past.
// This method is one bounded turn, not a poll timer, IDLE or a coordinator.
func (i *Inbox) Poll(ctx context.Context) (InboxProgress, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.tailReady {
		current := i.executor.SequenceVersion()
		sequence, ok := i.executor.rebaseSequence(i.tailVersion, current, i.tailSequence)
		if !ok {
			i.tailReady = false
		} else {
			i.tailSequence = sequence
			i.tailVersion = current
		}
	}
	if !i.tailReady || i.continuing || len(i.pending) > 0 || len(i.deferred) > 0 {
		return i.sweepLocked(ctx)
	}
	var progress InboxProgress
	if len(i.seen) >= i.limits.KnownUIDs {
		// Rebase does not prune remembered UIDs. At the original cache bound,
		// actual inventory may retire discovery entries for deleted mail.
		// It supplies no financial release or larger metadata allowance.
		return i.sweepLocked(ctx)
	}
	if i.tailUID == ^uint32(0) {
		i.tailReady = false
		return progress, ErrCapacity
	}
	next := i.tailUID + 1
	version := i.tailVersion
	command, err := i.executor.Fetch(ctx, next, next)
	if err != nil {
		return progress, err
	}
	bodies, err := command.Wait(ctx)
	if errors.Is(err, ErrRefused) {
		i.tailReady = false
		progress.More = true
		return progress, nil
	}
	if err != nil {
		i.tailReady = false
		return progress, err
	}
	if len(bodies) == 0 {
		i.tailReady = false
		progress.More = true
		return progress, nil
	}
	if len(bodies) != 1 || bodies[0].UID != next || bodies[0].Sequence == 0 {
		i.tailReady = false
		return progress, ErrProtocol
	}
	progress.Observed = 1
	if i.seen[bodies[0].UID] {
		i.tailReady = false
		progress.More = true
		return progress, nil
	}
	step, _, err := i.acceptBody(bodies[0], i.admission())
	progress.Applied, progress.Deferred = step.Applied, step.Deferred
	if err != nil || progress.Deferred > 0 {
		i.tailReady = false
		progress.More = err == nil
		return progress, err
	}
	sequence, exact := i.executor.rebaseSequence(version, bodies[0].sequenceVersion, i.tailSequence)
	if !exact || sequence == ^uint32(0) || bodies[0].Sequence != sequence+1 {
		i.tailReady = false
		progress.More = true
	}
	i.tailUID = next
	i.tailSequence = bodies[0].Sequence
	i.tailVersion = bodies[0].sequenceVersion
	if !i.tailReady {
		progress.More = true
	}
	return progress, nil
}
