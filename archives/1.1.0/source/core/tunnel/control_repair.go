package tunnel

import (
	"context"
	"errors"
	"itom.local/core/mailbox"
	"time"
)

func (m *laneMail) controlClient() *mailbox.Client {
	if fresh := m.controlGeneration.Load(); fresh != nil {
		return fresh
	}
	return m.Client
}

// Overrides promoted methods so no caller keeps using the retired generation.
func (m *laneMail) Ensure(ctx context.Context, folder string) error {
	return m.controlClient().Ensure(ctx, folder)
}
func repairableControlFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, mailbox.ErrChanged) || errors.Is(err, mailbox.ErrScope) || errors.Is(err, mailbox.ErrLimit) {
		return false
	}
	switch mailbox.FailureReason(err) {
	case "eof", "reset", "socket_closed", "timeout", "unreachable", "refused", "bye":
		return true
	}
	return false
}

// This changes a socket, never the cipher/session or a journal balance. A failed
// APPEND remains uncertain; callers must issue any replacement as a NEW charged
// journal attempt and use fresh authenticated counters, rather than replay MIME.
func (m *laneMail) repairControl(ctx context.Context) error {
	m.controlRepairMu.Lock()
	defer m.controlRepairMu.Unlock()
	old := m.controlClient()
	if old.Ready() {
		return nil
	}
	if m.dial == nil {
		return errors.New("CONTROL_DIAL_UNAVAILABLE")
	}
	_ = old.Close()
	snapshot := old.SocketSnapshot("control_previous")
	m.previousControl.Store(&snapshot)
	// Four provider LOGINs run at once. A simultaneous fifteen-account EOF
	// must allow the last replacement to reach that bounded admission queue.
	call, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	fresh, err := dialControlReplacement(repairAdmission(controlAdmission(call)), m.dial)
	if err != nil {
		return err
	}
	m.writerMu.Lock()
	if m.closed || ctx.Err() != nil {
		m.writerMu.Unlock()
		fresh.Close()
		return context.Canceled
	}
	m.controlGeneration.Store(fresh)
	m.writerMu.Unlock()
	return nil
}

// A physical outage can outlast the first replacement LOGIN. Preserve the
// encrypted carrier and its streams within the existing 15-second repair
// budget; retry only failures that precede any application APPEND. The account
// gate still enforces its shared cooldown and bounded LOGIN admission.
func dialControlReplacement(ctx context.Context, dial func(context.Context) (*mailbox.Client, error)) (*mailbox.Client, error) {
	delay := 100 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fresh, err := dial(ctx)
		if err == nil {
			return fresh, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !repairableControlFailure(err) && !transientIMAPDialReason(mailbox.FailureReason(err)) && !errors.Is(err, errLocalConnectionLimit) && !connectionLimitFallback(err) {
			return nil, err
		}
		if err := dataRepairPause(ctx, delay); err != nil {
			return nil, err
		}
		delay = min(time.Second, 2*delay)
	}
}
