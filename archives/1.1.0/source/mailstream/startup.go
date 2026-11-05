package mailstream

import (
	"context"
	"errors"
	"time"

	imap "github.com/emersion/go-imap/v2"
)

func taggedNo(err error) bool {
	var status *imap.Error
	return errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo
}

func missingMailbox(err error) bool {
	var status *imap.Error
	if !errors.As(err, &status) || status.Type != imap.StatusResponseTypeNo {
		return false
	}
	// Older providers omit NONEXISTENT. Explicit temporary/permission
	// response codes must never trigger a metadata mutation.
	return status.Code == "" || status.Code == imap.ResponseCodeNonExistent || status.Code == imap.ResponseCodeTryCreate
}

// Retry only accounts that have never entered service. Published queues,
// replay state and established readers are never replaced by this loop.
// Each failed attempt closes its sockets before releasing the login gate.
func (a *mailAccountState) startControls() (*mailPipe, *mailPipe, error) {
	for attempt := uint32(0); ; attempt++ {
		a.mu.Lock()
		a.startupAttempts++
		a.startupRetryAt = time.Time{}
		a.failure = ""
		a.mu.Unlock()
		control, writer, err := a.openControls()
		if err == nil || a.ctx.Err() != nil || errors.Is(err, ErrAuth) || errors.Is(err, ErrStorage) {
			return control, writer, err
		}
		delay := time.Second * time.Duration(1<<min(attempt, 4))
		// Account-specific staggering keeps retries from becoming a new
		// simultaneous provider connection burst after a shared outage.
		for _, b := range a.config.ID {
			delay += time.Duration(b%17) * time.Millisecond
		}
		a.mu.Lock()
		a.startupRetryAt = time.Now().Add(delay)
		a.mu.Unlock()
		a.link.notify()
		timer := time.NewTimer(delay)
		select {
		case <-a.ctx.Done():
			timer.Stop()
			return nil, nil, context.Cause(a.ctx)
		case <-timer.C:
		}
	}
}

func (a *mailAccountState) openControls() (control, writer *mailPipe, err error) {
	select {
	case a.link.login <- struct{}{}:
	case <-a.ctx.Done():
		return nil, nil, a.ctx.Err()
	}
	defer func() {
		if err != nil && control != nil {
			control.close()
		}
		<-a.link.login
	}()
	stage := "control_login"
	defer func() {
		if err != nil {
			a.failedStage(stage, err)
		}
	}()
	a.phase(stage)
	control, err = openMailPipe(a.ctx, a.config)
	if err != nil {
		return
	}
	control.onPacket = func(packet []byte) {
		a.downBytes.Add(uint64(len(packet)))
		a.link.note("received", a.config.ID, packet, 0, true)
	}
	if !a.link.client {
		control.onControlConsumed = a.controlConsumed
	}
	control.uidControl.Store(true)
	stage = "control_select"
	a.phase(stage)
	stop := control.limit(15 * time.Second)
	_, err = control.selectLog(a.link.controlFolder(false), true)
	stop()
	if err != nil {
		return
	}
	// STATUS first: do not CREATE an already-existing outgoing namespace.
	stage = "control_inventory"
	a.phase(stage)
	folder := a.link.controlFolder(true)
	options := &imap.StatusOptions{UIDNext: true, UIDValidity: true}
	stop = control.limit(15 * time.Second)
	initial, statusErr := control.client.Status(folder, options).Wait()
	stop()
	if missingMailbox(statusErr) {
		stage = "control_create_outgoing"
		a.phase(stage)
		stop = control.limit(15 * time.Second)
		err = control.client.Create(folder, nil).Wait()
		stop()
		if err != nil && !taggedNo(err) {
			err = ErrClosed
			return
		}
		err = nil // simultaneous peer creation is an ordinary tagged refusal
		stage = "control_inventory"
		a.phase(stage)
		stop = control.limit(15 * time.Second)
		initial, statusErr = control.client.Status(folder, options).Wait()
		stop()
	}
	if statusErr != nil {
		err = ErrClosed
		return
	}
	if initial == nil || initial.UIDNext == 0 || initial.UIDValidity == 0 {
		err = ErrProtocol
		return
	}
	a.mu.Lock()
	a.maintenance.initialOut = uidWatermark{initial.UIDValidity, uint32(initial.UIDNext) - 1}
	a.maintenance.writes = make(map[imap.UID]controlWrite)
	a.mu.Unlock()
	if a.link.client {
		stage = "writer_login"
		a.phase(stage)
		writer, err = openMailPipe(a.ctx, a.config)
	}
	return
}
