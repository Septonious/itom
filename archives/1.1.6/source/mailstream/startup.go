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
	}()
	stage := "control_login"
	defer func() {
		if err != nil {
			a.failedStage(stage, err)
		}
	}()
	a.phase(stage)
	control, err = openMailPipe(a.ctx, a.config)
	// The 4-slot login gate protects actual TLS/LOGIN operations only.
	// Releasing it before provider LIST/CREATE/SELECT prevents a stalled
	// 15-second CREATE on an unavailable account from starving authenticated
	// session attachment on an already-ready account.
	<-a.link.login
	if err != nil {
		return
	}
	// Metadata mutations retain their own four-operation bound so this
	// fix does not introduce a burst of concurrent provider CREATEs.
	select {
	case a.link.metadata <- struct{}{}:
		defer func() { <-a.link.metadata }()
	case <-a.ctx.Done():
		err = a.ctx.Err()
		return
	}
	control.pollTraffic = &a.link.lastUserTraffic
	control.onPacket = func(packet []byte) {
		a.link.lastUserTraffic.Store(time.Now().UnixNano())
		a.downBytes.Add(uint64(len(packet)))
		a.link.note("received", a.config.ID, packet, 0, true)
	}
	if !a.link.client {
		control.onControlConsumed = a.controlConsumed
	}
	control.uidControl.Store(true)
	// A missing-folder SELECT/STATUS can stall instead of returning NONEXISTENT.
	// Inventory the two owned control names with LIST first; do not CREATE
	// existing folders, since some providers stall that redundant mutation too.
	stage = "control_list"
	a.phase(stage)
	stop := control.limit(15 * time.Second)
	folders, listErr := control.client.List("", a.link.folderPrefix()+"-c-*", nil).Collect()
	stop()
	if listErr != nil {
		err = listErr
		return
	}
	for n, folder := range []string{a.link.controlFolder(false), a.link.controlFolder(true)} {
		if listedControlFolder(folders, folder) {
			continue
		}
		stage = []string{"control_create_incoming", "control_create_outgoing"}[n]
		a.phase(stage)
		stop = control.limit(15 * time.Second)
		err = control.client.Create(folder, nil).Wait()
		stop()
		if err == nil {
			continue
		}
		if !taggedNo(err) {
			return
		}
		// The peer may have created it after our LIST. Verify presence with
		// LIST before SELECT; a permission refusal is not proof of existence.
		createErr := err
		stage = "control_verify_creation"
		a.phase(stage)
		stop = control.limit(15 * time.Second)
		found, verifyErr := control.client.List("", folder, nil).Collect()
		stop()
		if verifyErr != nil {
			err = verifyErr
			return
		}
		if !listedControlFolder(found, folder) {
			err = createErr
			return
		}
		err = nil
	}
	stage = "control_select"
	a.phase(stage)
	stop = control.limit(15 * time.Second)
	_, err = control.selectLog(a.link.controlFolder(false), true)
	stop()
	if err != nil {
		return
	}
	stage = "control_inventory"
	a.phase(stage)
	options := &imap.StatusOptions{UIDNext: true, UIDValidity: true, NumMessages: true}
	stop = control.limit(15 * time.Second)
	initial, statusErr := control.client.Status(a.link.controlFolder(true), options).Wait()
	stop()
	if statusErr != nil {
		err = statusErr
		return
	}
	if initial == nil || initial.UIDNext == 0 || initial.UIDValidity == 0 || initial.NumMessages == nil {
		err = ErrProtocol
		return
	}
	a.mu.Lock()
	a.maintenance.initialOut = uidWatermark{initial.UIDValidity, uint32(initial.UIDNext) - 1}
	a.maintenance.writes = make(map[imap.UID]controlWrite)
	a.maintenance.initialMessages = *initial.NumMessages
	a.maintenance.pendingMessages = 0
	a.mu.Unlock()
	// Both peers bootstrap on their selected control connection. Independent
	// APPEND writers are opened only after authenticated session admission.
	return
}

func listedControlFolder(folders []*imap.ListData, name string) bool {
	for _, folder := range folders {
		if folder.Mailbox == name {
			return true
		}
	}
	return false
}
