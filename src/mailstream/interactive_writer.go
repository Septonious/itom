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
		batch := []mailWork{work}
		if work.feedback && !work.spill {
			_, id, _, _ := parseHeader(work.body)
			size := 10 + len(work.body)
		collect:
			for len(batch) < 16 {
				select {
				case next := <-a.interactive:
					_, nextID, _, err := parseHeader(next.body)
					if !next.feedback || next.spill || next.target != work.target || err != nil || nextID != id || size+4+len(next.body) > maxMailPlain {
						carry = &next
						break collect
					}
					size += 4 + len(next.body)
					batch = append(batch, next)
				default:
					break collect
				}
			}
		}
		if err := a.writeInteractiveBatch(batch); err != nil && a.ctx.Err() == nil {
			a.failedStage("interactive_append", err)
			// Leave the charge for an uncertain publication intact. Closing
			// this socket wakes the ordinary writer's recovery owner.
			work.target.conn.NetConn().Close()
		}
	}
}

func (a *mailAccountState) writeInteractive(work mailWork) error {
	return a.writeInteractiveBatch([]mailWork{work})
}

func (a *mailAccountState) writeInteractiveBatch(batch []mailWork) error {
	work := batch[0]
	published := false
	defer func() {
		if work.spill && !published {
			a.feedbackSpill.Store(false)
		}
	}()
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
		for _, w := range batch {
			a.dropInteractive(w)
		}
		return nil
	case <-a.ctx.Done():
		for _, w := range batch {
			a.dropInteractive(w)
		}
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
	packets := make([][]byte, 0, len(batch))
	charge, total := 0, 0
	for _, w := range batch {
		_, packetID, _, err := parseHeader(w.body)
		if err != nil || packetID != id {
			return ErrProtocol
		}
		packets = append(packets, w.body)
		charge += w.charge
		total += len(w.body)
	}
	encoded := encodeMail(work.body)
	if len(packets) > 1 {
		encoded = encodeMail(packMail(packets))
	}
	a.mu.Lock()
	a.queuedBytes -= charge
	if a.id != id || a.controlWriter != p || a.parking.Load() || a.ctx.Err() != nil {
		a.controlQueued -= len(batch)
		a.mu.Unlock()
		return nil
	}
	select {
	case <-p.done:
		a.controlQueued -= len(batch)
		a.mu.Unlock()
		return nil
	default:
	}
	a.reserveControlPublicationLocked(len(batch))
	err = a.chargeLocked(len(encoded))
	a.mu.Unlock()
	if err != nil {
		return err
	}
	for _, packet := range packets {
		a.link.note("append", a.config.ID, packet, 0, true)
	}
	started := time.Now()
	cmd, err := p.startAppend(a.link.controlFolder(true), encoded)
	if err != nil {
		return err
	}
	// Once the literal is written, the next small request can be submitted
	// without waiting a provider round trip for this command's tagged result.
	p.interactiveWait.Add(1)
	inFlight = true
	published = true
	go func() {
		if work.spill {
			defer a.feedbackSpill.Store(false)
		}
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
		a.upBytes.Add(uint64(total))
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
