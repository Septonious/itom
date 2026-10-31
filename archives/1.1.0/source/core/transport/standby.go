package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrSelectedLaneClosed = errors.New("V2_SELECTED_LANE_CLOSED")

// Already selected, otherwise unused lanes need service while the anchor waits
// for a phone and negotiates. NOOP holds no grant, ciphertext or UID evidence.
// It never logs in, selects another folder, or replaces a failed connection.
type standbyGroup struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	stopping bool
	err      error
	lanes    []RosterLane
}

func selectedLaneError(l RosterLane) error {
	return fmt.Errorf("%w: account=%s plane=%d", ErrSelectedLaneClosed, l.Executor.account, l.Plane)
}

func startStandby(parent context.Context, fail context.CancelFunc, lanes []RosterLane, anchor *Executor) *standbyGroup {
	ctx, cancel := context.WithCancel(parent)
	g := &standbyGroup{cancel: cancel, done: make(chan struct{})}
	var workers sync.WaitGroup
	for _, l := range lanes {
		if l.Executor == anchor {
			continue
		}
		g.lanes = append(g.lanes, l)
		workers.Go(func() {
			x := l.Executor
			// A service lifetime, not an assumed network RTT or a longer deadline.
			interval := min(10*time.Second, x.limits.ProgressTimeout/3)
			if interval <= 0 {
				interval = time.Millisecond
			}
			timer := time.NewTimer(interval)
			defer timer.Stop()
			failed := func() {
				g.mu.Lock()
				report := !g.stopping && parent.Err() == nil && g.err == nil
				if report {
					g.err = selectedLaneError(l)
				}
				g.mu.Unlock()
				if report {
					fail()
				}
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-x.done:
					failed()
					return
				case <-timer.C:
				}
				command, err := x.noop(ctx, true)
				if err == nil {
					// An issued tag must drain before a handoff; cancellation only stops
					// the next NOOP. The original command watchdog still bounds this wait.
					_, err = command.Wait(context.Background())
				}
				if err != nil {
					failed()
					return
				}
				timer.Reset(interval)
			}
		})
	}
	go func() { workers.Wait(); close(g.done) }()
	return g
}

func (g *standbyGroup) requestStop() {
	g.mu.Lock()
	g.stopping = true
	g.mu.Unlock()
	g.cancel()
}
func (g *standbyGroup) failure() error { g.mu.Lock(); defer g.mu.Unlock(); return g.err }

func (g *standbyGroup) stop(ctx context.Context) error {
	g.requestStop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.done:
	}
	if err := g.failure(); err != nil {
		return err
	}
	for _, l := range g.lanes {
		l.Executor.mu.Lock()
		closed := l.Executor.closed != nil
		l.Executor.mu.Unlock()
		if closed {
			return selectedLaneError(l)
		}
	}
	return nil
}

// Only this owner's tagged, side-effect-free NOOP can span construction of a
// roster driver. Public commands and every data/metadata operation must drain.
func (e *Executor) onlyStandbyPendingLocked() bool {
	for _, op := range e.pending {
		if op.class != commandNoop || !op.standby {
			return false
		}
	}
	return true
}

// KeepSelectedLanes services otherwise unowned selected sockets while another
// account is still opening or validating inventory. Its only commands are the
// existing side-effect-free NOOPs. Stop and join before transferring ownership;
// on cancellation close the owning executors before joining an issued tag.
// It does not open/reconnect sockets, spend quota, or extend a command deadline.
func KeepSelectedLanes(ctx context.Context, failed context.CancelFunc, lanes []RosterLane) func(context.Context) error {
	group := startStandby(ctx, failed, lanes, nil)
	return group.stop
}
