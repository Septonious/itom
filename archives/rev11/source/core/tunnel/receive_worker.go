package tunnel

import (
	"context"
	"errors"
	"itom.local/core/mailbox"
	"sync/atomic"
	"time"
)

const receiveProgressTimeout = 1500 * time.Millisecond

func (m *laneMail) ReaderHandoffs() uint64 { return 0 }
func (m *laneMail) ReaderRepairs() uint64  { return m.readerRepairs.Load() }
func (m *laneMail) readerRef() *mailbox.Client {
	if m == nil {
		return nil
	}
	m.readerStateMu.Lock()
	defer m.readerStateMu.Unlock()
	return m.reader
}
func (m *laneMail) availableReader(_ string) *mailbox.Client { return m.readerRef() }

// Transport progress extends the idle deadline; a complete large BODY may take
// longer than this deadline. The underlying command retains its 30s absolute
// cap. Guard and command are joined before replacing a physical receive role.
func guardReceive(ctx context.Context, reader *mailbox.Client, budget time.Duration, fn func(context.Context) error) error {
	return guardReceivePaused(ctx, reader, budget, nil, fn)
}
func guardReceivePaused(ctx context.Context, reader *mailbox.Client, budget time.Duration, paused *atomic.Bool, fn func(context.Context) error) error {
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done, joined := make(chan struct{}), make(chan struct{})
	var timedOut atomic.Bool
	go func() {
		defer close(joined)
		last := time.Now()
		tick := time.NewTicker(min(100*time.Millisecond, budget/4))
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-commandCtx.Done():
				return
			case now := <-tick.C:
				if paused != nil && paused.Load() {
					last = now
					continue
				}
				if at := reader.LastReadProgress(); at.After(last) {
					last = at
				}
				if now.Sub(last) >= budget {
					timedOut.Store(true)
					reader.InterruptReceive()
					cancel()
					return
				}
			}
		}
	}()
	err := fn(commandCtx)
	close(done)
	<-joined
	if timedOut.Load() {
		return context.DeadlineExceeded
	}
	return err
}
func retryableReceive(err error) bool {
	if err == nil || errors.Is(err, mailbox.ErrScope) || errors.Is(err, mailbox.ErrLimit) || errors.Is(err, mailbox.ErrChanged) {
		return false
	}
	switch mailbox.FailureReason(err) {
	case "eof", "reset", "socket_closed", "timeout", "bye", "canceled", "mailbox_state_changed":
		return true
	}
	return false
}

// Caller owns readerCommandMu; old command is already complete. UID discovery
// is copied with original expiry, without transferring physical proof/locators.
func (m *laneMail) repairReader(ctx context.Context, old *mailbox.Client, folder string) error {
	if m.dial == nil {
		return errors.New("RECEIVE_DIAL_UNAVAILABLE")
	}
	discovery := old.TailDiscovery(folder)
	_ = old.Close()
	// Retain one bounded failed generation after replacement so diagnostics
	// cannot lose its first close cause when the current role is healthy again.
	previous := old.SocketSnapshot("receive_previous")
	m.previousReader.Store(&previous)
	m.readerStateMu.Lock()
	if m.reader == old {
		m.reader = nil
	}
	m.readerStateMu.Unlock()
	for ctx.Err() == nil {
		m.writerMu.Lock()
		stopped := m.closed
		parent := m.dataCtx
		m.writerMu.Unlock()
		if stopped || parent != nil && parent.Err() != nil {
			return context.Canceled
		}
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		stop := func() bool { return false }
		if parent != nil {
			stop = context.AfterFunc(parent, cancel)
		}
		fresh, err := m.dial(repairAdmission(dialCtx))
		stop()
		cancel()
		if err == nil {
			if err = fresh.RestoreTailDiscovery(discovery); err != nil {
				_ = fresh.Close()
				return err
			}
			m.writerMu.Lock()
			stopped = m.closed
			if stopped || ctx.Err() != nil || parent != nil && parent.Err() != nil {
				m.writerMu.Unlock()
				_ = fresh.Close()
				return context.Canceled
			}
			m.readerStateMu.Lock()
			m.reader = fresh
			m.readerStateMu.Unlock()
			m.writerMu.Unlock()
			m.readerCheck = time.Time{}
			m.readerRepairs.Add(1)
			return nil
		}
		if !retryableReceive(err) && !connectionLimitFallback(err) {
			return err
		}
		if pause(ctx, 250*time.Millisecond) != nil {
			return ctx.Err()
		}
	}
	return ctx.Err()
}
func (m *laneMail) readPage(ctx context.Context, f string, cur mailbox.Cursor, verify bool, limit uint32, emit func(mailbox.Message) error) (*mailbox.Client, []mailbox.Message, mailbox.Cursor, error) {
	for {
		reader := m.readerRef()
		if reader == nil {
			return nil, nil, cur, errors.New("RECEIVE_WORKER_UNAVAILABLE")
		}
		var rows []mailbox.Message
		next := cur
		err := m.command(ctx, workReceive, func() error {
			var paused atomic.Bool
			return guardReceivePaused(ctx, reader, m.receiveProgressBudget(), &paused, func(cmd context.Context) error {
				var e error
				callback := emit
				if emit != nil {
					callback = func(msg mailbox.Message) error {
						paused.Store(true)
						defer paused.Store(false)
						if err := cmd.Err(); err != nil {
							return err
						}
						return emit(msg)
					}
				}
				rows, next, e = reader.PageTailStream(cmd, f, cur, verify, limit, callback)
				return e
			})
		})
		if err == nil {
			return reader, rows, next, nil
		}
		if ctx.Err() != nil || !retryableReceive(err) {
			return reader, nil, cur, err
		}
		if err = m.repairReader(ctx, reader, f); err != nil {
			return reader, nil, cur, err
		}
		verify = true
	}
}
func (m *laneMail) receiveWait(ctx context.Context, f string, maxWait time.Duration, wake <-chan struct{}) (used, notified, kicked bool, err error) {
	m.readerCommandMu.Lock()
	defer m.readerCommandMu.Unlock()
	reader := m.readerRef()
	if reader == nil || f != m.recv || !reader.IdleCapable() {
		return false, false, false, nil
	}
	reader.SetReceiveProgressBudget(m.receiveProgressBudget())
	err = m.command(ctx, workReceive, func() error {
		return guardReceive(ctx, reader, maxWait+m.receiveProgressBudget(), func(_ context.Context) error {
			var e error
			// DONE is written before the next FETCH. Join the tagged completion
			// before exposing that FETCH's result, while overlapping propagation.
			// Its cancellation belongs to the lane lifetime: the guard's temporary
			// command context is canceled on return and must not kill pending DONE.
			// A guard timeout still interrupts the reader socket directly.
			notified, kicked, e = reader.IdleWaitWakeAhead(ctx, f, maxWait, wake)
			return e
		})
	})
	if err != nil && ctx.Err() == nil && retryableReceive(err) {
		err = m.repairReader(ctx, reader, f)
		return false, false, false, err
	}
	// EXISTS is a wake, not a new UIDVALIDITY/UIDNEXT proof. Keep the bounded
	// selection renewal cadence; selecting again after every notification would
	// add a full provider round trip to every active receive cycle.
	return true, notified, kicked, err
}

// Only an unloaded authenticated NOOP calibrates this bound. DATA/FETCH
// congestion cannot raise it. The adapter still enforces its 30s absolute cap.
func (m *laneMail) Ping(ctx context.Context) (time.Duration, error) {
	rtt, err := m.controlClient().Ping(ctx)
	if err == nil {
		m.providerRTT.Store(int64(rtt))
	}
	return rtt, err
}
func (m *laneMail) receiveProgressBudget() time.Duration {
	return min(10*time.Second, max(receiveProgressTimeout, time.Second+6*time.Duration(m.providerRTT.Load())))
}
