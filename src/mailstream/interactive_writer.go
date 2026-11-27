package mailstream

import "time"

// The companion APPEND connection already belongs to this account. One
// independent publisher lets interactive traffic progress during DATA literal
// continuation, rollover and cleanup. Each writer has two bounded tag slots.
func (a *mailAccountState) publishInteractive() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case work := <-a.interactive:
			if err := a.writeInteractive(work); err != nil && a.ctx.Err() == nil {
				a.failedStage("interactive_append", err)
				// Leave the charge for an uncertain publication intact. Closing
				// this socket wakes the ordinary writer's recovery owner.
				work.target.conn.NetConn().Close()
			}
		}
	}
}

func (a *mailAccountState) writeInteractive(work mailWork) error {
	p := work.target
	dequeued := time.Now()
	if !work.queuedAt.IsZero() {
		a.interactiveQueueUS.Store(dequeued.Sub(work.queuedAt).Microseconds())
	}
	p.interactiveMu.Lock()
	defer p.interactiveMu.Unlock()
	select {
	case p.interactiveTags <- struct{}{}:
	case <-p.done:
		a.dropInteractive(work)
		return nil
	case <-a.ctx.Done():
		a.dropInteractive(work)
		return nil
	}
	a.interactiveTagUS.Store(time.Since(dequeued).Microseconds())
	inFlight := false
	defer func() {
		if !inFlight {
			<-p.interactiveTags
		}
	}()
	_, id, _, err := parseHeader(work.body)
	if err != nil {
		return err
	}
	encoded := encodeMail(work.body)
	a.mu.Lock()
	a.queuedBytes -= work.charge
	if a.id != id || a.controlWriter != p || a.parking.Load() || a.ctx.Err() != nil {
		a.controlQueued--
		a.mu.Unlock()
		return nil
	}
	select {
	case <-p.done:
		a.controlQueued--
		a.mu.Unlock()
		return nil
	default:
	}
	a.reserveControlPublicationLocked(1)
	err = a.chargeLocked(len(encoded))
	a.mu.Unlock()
	if err != nil {
		return err
	}
	a.link.note("append", a.config.ID, work.body, 0, true)
	started := time.Now()
	cmd, err := p.startAppend(a.link.controlFolder(true), encoded)
	if err != nil {
		return err
	}
	// Once the literal is written, the next small request can be submitted
	// without waiting a provider round trip for this command's tagged result.
	p.interactiveWait.Add(1)
	inFlight = true
	go func() {
		defer p.interactiveWait.Done()
		defer func() { <-p.interactiveTags }()
		result, err := cmd.Wait()
		if err != nil {
			select {
			case <-p.done:
				return
			default:
			}
			if a.ctx.Err() == nil {
				a.failedStage("interactive_append", err)
				p.conn.NetConn().Close()
			}
			return
		}
		a.interactiveAppendUS.Store(time.Since(started).Microseconds())
		a.observeControlPublication(time.Since(started))
		a.controlAppended(result, len(encoded))
		a.appendCompleted(true, len(encoded), started)
		a.upBytes.Add(uint64(len(work.body)))
	}()
	return nil
}

func (a *mailAccountState) dropInteractive(work mailWork) {
	a.mu.Lock()
	a.controlQueued--
	a.queuedBytes -= work.charge
	a.mu.Unlock()
}

func (p *mailPipe) closeInteractiveWriter() {
	p.close()
	p.interactiveMu.Lock()
	p.interactiveWait.Wait()
	p.interactiveMu.Unlock()
}
