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
	fresh, err := m.dial(repairAdmission(controlAdmission(call)))
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
