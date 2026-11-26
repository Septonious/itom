package mailstream

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"time"
)

var errReaderPaused = errors.New("MAILSTREAM_READER_PAUSED")

// A reader checkpoint is captured only by the owner after the receive loop
// has returned at a complete FETCH/IDLE boundary. It is not a new epoch.
type readerCheckpoint struct {
	pipe   *mailPipe
	folder string
}

func (a *mailAccountState) dataCallback(p *mailPipe) {
	p.onPacket = func(packet []byte) {
		if len(packet) > 4096 {
			a.link.lastBulk.Store(time.Now().UnixNano())
		}
		a.downBytes.Add(uint64(len(packet)))
		a.link.note("received", a.config.ID, packet, p.generation, false)
	}
}
func (a *mailAccountState) startReader(p *mailPipe, data bool) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := p.receive(a.ctx, a.link.incoming); err != nil && a.ctx.Err() == nil {
			a.mu.Lock()
			current := a.control == p || a.data == p
			a.mu.Unlock()
			if current {
				stage := "control_receive"
				if data {
					stage = "data_receive"
				}
				a.failedStage(stage, err)
				a.fail()
			}
		}
	}()
	return done
}
func pauseReader(ctx context.Context, p *mailPipe, done chan struct{}) (*readerCheckpoint, error) {
	if p == nil || done == nil {
		return nil, ErrClosed
	}
	select {
	case p.pauseRead <- struct{}{}:
		<-done // the owner accepted a boundary; no command is still executing
	case <-done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	box := p.client.Mailbox()
	if box == nil || p.invalid.Load() {
		return nil, ErrProtocol
	}
	return &readerCheckpoint{pipe: p, folder: box.Name}, nil
}
func (a *mailAccountState) reopenReader(ctx context.Context, c *readerCheckpoint, data bool, id SessionID) (*mailPipe, error) {
	p, err := openMailPipeAttempt(ctx, a.ctx, a.config)
	if err != nil {
		return nil, err
	}
	old := c.pipe
	p.uidControl.Store(old.uidControl.Load())
	stop := context.AfterFunc(ctx, p.close)
	sel, err := p.client.Select(c.folder, nil).Wait()
	if !stop() || err != nil || sel == nil || sel.UIDValidity != old.validity || (!data && uint32(sel.UIDNext) <= old.after) || (data && sel.NumMessages < old.after) {
		p.close()
		return nil, ErrProtocol
	}
	p.validity = old.validity
	p.after = old.after
	p.confirmed = sel.NumMessages
	p.cursor.Store(old.after)
	p.fetchBatch = old.fetchBatch
	p.generation = old.generation
	p.slot = old.slot
	p.waitingRoute = old.waitingRoute
	p.generationDebug.Store(old.generation)
	p.consumed = append(imap.UIDSet(nil), old.consumed...)
	p.consumedMIMEBytes.Store(old.consumedMIMEBytes.Load())
	p.knownFolders = map[string]bool{c.folder: true}
	p.service = old.service
	if data {
		a.dataCallback(p)
		a.configureSegments(p, id)
	} else {
		p.onPacket = old.onPacket
		p.onControlConsumed = old.onControlConsumed
	}
	return p, nil
}

// Called by the sole writer actor. Keep DATA append counters and segment
// ownership in that actor; a pause never re-enters attach or resets a cursor.
func (a *mailAccountState) parkReaders(ctx context.Context, control **mailPipe, readDone *chan struct{}, writer **mailPipe, completed chan error, inFlight *int, parkedControl, parkedData **readerCheckpoint) error {
	if !a.link.client || !a.scaleBlocked.Load() {
		return ErrProtocol
	}
	if *parkedControl != nil || *control == nil {
		return nil
	}
	a.mu.Lock()
	data, dd, companion := a.data, a.dataDone, a.controlWriter
	queued := a.queuedBytes
	a.mu.Unlock()
	if queued != 0 {
		return ErrCapacity
	}
	for *inFlight > 0 {
		select {
		case err := <-completed:
			*inFlight--
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-a.ctx.Done():
			return ErrClosed
		}
	}
	if companion != nil {
		if !companion.interactiveMu.TryLock() {
			return ErrCapacity
		}
		tags := len(companion.interactiveTags)
		companion.interactiveMu.Unlock()
		if tags != 0 {
			return ErrCapacity
		}
	}
	var d *readerCheckpoint
	var err error
	if data != nil {
		d, err = pauseReader(ctx, data, dd)
		if err != nil {
			return err
		}
	}
	c, err := pauseReader(ctx, *control, *readDone)
	if err != nil {
		if d != nil {
			a.mu.Lock()
			a.dataDone = a.startReader(data, true)
			a.mu.Unlock()
		}
		return err
	}
	a.mu.Lock()
	a.parking.Store(true)
	a.ready = false
	a.stage = "parked"
	if a.unallocated.Load() {
		a.stage = "unallocated"
	}
	a.control, a.data, a.writer, a.controlWriter = nil, nil, nil, nil
	a.dataDone = nil
	stopData := a.dataStop
	a.dataStop = nil
	a.mu.Unlock()
	if stopData != nil {
		stopData()
	}
	if data != nil {
		data.close()
	}
	(*control).close()
	if *writer != nil {
		(*writer).close()
	}
	if companion != nil {
		companion.closeInteractiveWriter()
	}
	*control, *writer, *readDone = nil, nil, nil
	*parkedControl, *parkedData = c, d
	a.link.notify()
	return nil
}
func (a *mailAccountState) resumeReaders(ctx context.Context, control **mailPipe, readDone *chan struct{}, writer **mailPipe, parkedControl, parkedData **readerCheckpoint) error {
	if *parkedControl == nil {
		if *control != nil {
			return nil
		}
		// This account was parked before its first control selection, so
		// it has no previous cursor or DATA generation to restore.
		c, w, err := a.openControls(ctx)
		if err != nil {
			return err
		}
		a.mu.Lock()
		a.control, a.writer, a.ready = c, w, true
		a.stage, a.failure = "serving", ""
		a.parking.Store(false)
		a.mu.Unlock()
		*control, *writer, *readDone = c, w, a.startReader(c, false)
		a.link.notify()
		return nil
	}
	select {
	case a.link.login <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.ctx.Done():
		return ErrClosed
	}
	defer func() { <-a.link.login }()
	a.mu.Lock()
	id := a.id
	a.mu.Unlock()
	c, e := a.reopenReader(ctx, *parkedControl, false, id)
	if e != nil {
		return e
	}
	var d, w, cw *mailPipe
	success := false
	defer func() {
		if !success {
			for _, p := range []*mailPipe{c, d, w, cw} {
				if p != nil {
					p.close()
				}
			}
		}
	}()
	if *parkedData != nil {
		d, e = a.reopenReader(ctx, *parkedData, true, id)
		if e != nil {
			return e
		}
		w, e = openMailPipeAttempt(ctx, a.ctx, a.config)
		if e != nil {
			return e
		}
		cw, e = openMailPipeAttempt(ctx, a.ctx, a.config)
		if e != nil {
			return e
		}
	}
	a.mu.Lock()
	a.control, a.data, a.writer, a.controlWriter = c, d, w, cw
	a.ready = true
	a.stage = "serving"
	a.parking.Store(false)
	if d != nil {
		a.dataDone = a.startReader(d, true)
	}
	a.mu.Unlock()
	*control, *writer, *readDone = c, w, a.startReader(c, false)
	*parkedControl, *parkedData = nil, nil
	success = true
	a.link.notify()
	return nil
}
func (a *mailAccountState) scale(ctx context.Context, enable bool) error {
	op := writerOperation{scale: -1, scaleCtx: ctx, done: make(chan error, 1)}
	if enable {
		op.scale = 1
	}
	select {
	case a.writerOps <- op:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.ctx.Done():
		return ErrClosed
	}
	// Once accepted, join the operation; the operation itself observes ctx.
	select {
	case err := <-op.done:
		return err
	case <-a.ctx.Done():
		return ErrClosed
	}
}
