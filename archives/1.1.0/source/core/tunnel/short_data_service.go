package tunnel

import (
	"context"
	"crypto/sha256"
	"itom.local/core/mailbox"
)

// Protect one real split DATA receiver from bulk; small pools retain legacy
// service. The actual socket budget and all durable accounting stay unchanged.
func (l *lane) protectedDataReader() bool {
	if l.stripe == nil || l.dataShards == 0 {
		return false
	}
	l.stripe.mu.Lock()
	defer l.stripe.mu.Unlock()
	reserve := l.stripe.interactiveReadersLocked()
	return reserve[0] == l || reserve[1] == l
}
func (m *laneMail) wakeDataReader() {
	if m.dataReaderWake != nil {
		select {
		case m.dataReaderWake <- struct{}{}:
		default:
		}
	}
}

// FETCH, IDLE and borrowed APPEND share this fair, cancellable socket lease.
// Running commands, including partially read BODYs, are never preempted.
func (m *laneMail) dataSocket(ctx context.Context, class accountWorkClass) (func(), error) {
	m.dataWork.mu.Lock()
	m.dataWork.capacity = 1
	m.dataWork.mu.Unlock()
	return m.dataWork.acquire(ctx, class)
}

type shortSocketLease struct {
	data    bool
	release func()
	err     error
}

// Race admission, never publication. Cancel and JOIN both contenders before
// invoking APPEND once. Unused permits are always released; cancellation of
// admission never owns the actual wire operation's context.
func (m *laneMail) shortSocket(ctx context.Context) (shortSocketLease, error) {
	return m.availableSocket(ctx, workUrgent)
}

func (m *laneMail) availableSocket(ctx context.Context, class accountWorkClass) (shortSocketLease, error) {
	if err := ctx.Err(); err != nil {
		return shortSocketLease{}, err
	}
	m.baseWork.mu.Lock()
	m.baseWork.capacity = 1
	m.baseWork.mu.Unlock()
	if release, ok := m.baseWork.tryAcquire(class); ok {
		return shortSocketLease{release: release}, nil
	}
	admission, cancel := context.WithCancel(ctx)
	defer cancel()
	m.wakeDataReader()
	results := make(chan shortSocketLease, 2)
	go func() {
		m.baseWork.mu.Lock()
		m.baseWork.capacity = 1
		m.baseWork.mu.Unlock()
		release, err := m.baseWork.acquire(admission, class)
		results <- shortSocketLease{release: release, err: err}
	}()
	go func() {
		release, err := m.dataSocket(admission, class)
		results <- shortSocketLease{data: true, release: release, err: err}
	}()
	first := <-results
	if first.err == nil {
		cancel()
	}
	second := <-results
	winner := first
	if winner.err != nil {
		winner = second
	} else if second.release != nil {
		second.release()
	}
	if winner.err != nil {
		return shortSocketLease{}, winner.err
	}
	if err := ctx.Err(); err != nil {
		winner.release()
		return shortSocketLease{}, err
	}
	return winner, nil
}
func (m *laneMail) appendShortData(ctx context.Context, folder string, raw []byte) error {
	return m.appendAvailableData(ctx, workUrgent, folder, raw)
}

// The capped phone can lend a quiet receiver to bulk as well. This uses the
// normal DATA class and existing physical slots; actual receive BODYs cannot be
// interrupted. It adds no connection, write permit, or uncertain-publication retry.
func (m *laneMail) appendAvailableData(ctx context.Context, class accountWorkClass, folder string, raw []byte) error {
	return m.command(ctx, class, func() error {
		lease, err := m.availableSocket(ctx, class)
		if err != nil {
			return err
		}
		var writer *mailbox.Client
		if lease.data {
			writer = m.dataReaderRef()
		} else {
			writer = m.controlClient()
		}
		if writer == nil || !writer.Ready() {
			lease.release()
			// No wire write occurred. Receiver repair belongs to its service owner;
			// preserve ordinary control fallback without nesting account admission.
			if !m.controlClient().Ready() {
				if err := m.repairControl(ctx); err != nil {
					return err
				}
			}
			return m.sharedSocketCommand(ctx, class, func() error { return m.controlClient().Append(ctx, folder, raw) })
		}
		err = writer.Append(ctx, folder, raw)
		if err == nil && lease.data {
			body, decodeErr := mailbox.Decode(raw)
			if decodeErr == nil {
				hints := writer.AppendHints(folder, map[[32]byte]bool{sha256.Sum256(body): true})
				m.borrowHintMu.Lock()
				m.borrowHints = append(m.borrowHints, hints...)
				if len(m.borrowHints) > 256 {
					m.borrowHints = append([]mailbox.AppendHint(nil), m.borrowHints[len(m.borrowHints)-256:]...)
				}
				m.borrowHintMu.Unlock()
			}
			clear(body)
		}
		if err != nil && lease.data {
			m.retireDataReader(writer, m.recv+"-data-00")
		}
		lease.release()
		return err // UNKNOWN is never resent here
	})
}
