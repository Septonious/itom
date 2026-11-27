package mailstream

import "time"

// The companion APPEND connection already belongs to this account. One
// independent publisher lets interactive traffic progress during DATA literal
// continuation, rollover and cleanup. Each writer has two bounded tag slots.
func (a *mailAccountState) publishInteractive() {
	var carry *mailWork
	for {
		var work mailWork
		if carry != nil {
			work, carry = *carry, nil
		} else {
			select {
			case <-a.ctx.Done():
				return
			case work = <-a.interactive:
			}
		}
		next, err := a.writeInteractive(work)
		carry = next
		if err != nil && a.ctx.Err() == nil {
			a.failedStage("interactive_append", err)
			// Leave the charge for an uncertain publication intact. Closing this
			// socket wakes the ordinary writer's recovery owner.
			work.target.conn.NetConn().Close()
		}
	}
}

// Already queued small packets for this publisher can share one MIME message.
// Do not add any coalescing timer or change route choice: an isolated request
// still publishes immediately and account-pinned traffic stays on its account.
// Stay below LITERAL- size after MIME encoding; preserve the next accepted
// packet as carry if a publication/identity boundary is encountered.
func collectInteractive(first mailWork, q <-chan mailWork) ([]mailWork, *mailWork) {
	batch := []mailWork{first}
	size := 6 + 4 + len(first.body)
	_, id, _, _ := parseHeader(first.body)
	for len(batch) < 8 && size < 2048 {
		select {
		case next := <-q:
			_, nextID, _, err := parseHeader(next.body)
			if err != nil || nextID != id || next.target != first.target || size+4+len(next.body) > 2048 {
				return batch, &next
			}
			batch = append(batch, next)
			size += 4 + len(next.body)
		default:
			return batch, nil
		}
	}
	return batch, nil
}

func (a *mailAccountState) writeInteractive(work mailWork) (*mailWork, error) {
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
		return nil, nil
	case <-a.ctx.Done():
		a.dropInteractive(work)
		return nil, nil
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
		a.dropInteractive(work)
		return nil, err
	}
	batch, carry := collectInteractive(work, a.interactive)
	packets := make([][]byte, 0, len(batch))
	charge := 0
	for _, item := range batch {
		packets = append(packets, item.body)
		charge += item.charge
	}
	encoded := encodeMail(packMail(packets))
	a.mu.Lock()
	a.queuedBytes -= charge
	if a.id != id || a.controlWriter != p || a.parking.Load() || a.ctx.Err() != nil {
		a.controlQueued -= len(batch)
		a.mu.Unlock()
		return carry, nil
	}
	select {
	case <-p.done:
		a.controlQueued -= len(batch)
		a.mu.Unlock()
		return carry, nil
	default:
	}
	a.reserveControlPublicationLocked(len(batch))
	err = a.chargeLocked(len(encoded))
	a.mu.Unlock()
	if err != nil {
		return carry, err
	}
	for _, packet := range packets {
		a.link.note("append", a.config.ID, packet, 0, true)
	}
	started := time.Now()
	cmd, err := p.startAppend(a.link.controlFolder(true), encoded)
	if err != nil {
		return carry, err
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
		for _, packet := range packets {
			a.upBytes.Add(uint64(len(packet)))
		}
	}()
	return carry, nil
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
