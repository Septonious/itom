package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/session"
	"itom.local/core/wire"
	"time"
)

func (l *lane) logicalContext() context.Context {
	if l.ownerCtx != nil {
		return l.ownerCtx
	}
	return l.ctx
}

// No physical writer can enter after Close begins. Close joins admitted writers
// before releasing the durable journal lock to a replacement carrier.
func (l *lane) beginPhysicalWrite() bool {
	l.physicalMu.Lock()
	defer l.physicalMu.Unlock()
	if l.physicalClosing || l.quiescing.Load() || l.ctx.Err() != nil {
		return false
	}
	l.physicalWriters.Add(1)
	l.physicalActive.Add(1)
	return true
}

func (l *lane) endPhysicalWrite() {
	l.releasePhysicalWriter(true)
}
func (l *lane) releasePhysicalWriter(wake bool) {
	l.physicalActive.Add(-1)
	l.physicalWriters.Done()
	if wake && l.stripe != nil {
		l.stripe.signal()
	}
}

// A canceled/unavailable carrier is retryable only before APPEND and after every
// prepared reservation has been canceled successfully. Unknown APPEND is never
// converted into NotCommitted and never releases physical debt.
func (l *lane) retryBeforeAppend(owner *lane, jobs []*datapath.Job, results []datapath.Completion, err, cleanupErr error) []datapath.Completion {
	results = failBatch(jobs, results, errors.Join(err, cleanupErr))
	// A selected carrier can lose DATA availability or be detached before its
	// context is canceled. That race is also a definitive pre-APPEND rejection,
	// not a logical TCP failure. Keep failed cancellation and unknown I/O fatal
	// to this path; neither may become an uncharged retry.
	carrierUnavailable := l.ctx.Err() != nil || l.quiescing.Load() || (l.stripe != nil && errors.Is(err, errStripeUnavailable))
	if owner != nil && owner.logicalContext().Err() == nil && carrierUnavailable && cleanupErr == nil {
		for i := range results {
			results[i].NonFatal = results[i].Outcome == flow.NotCommitted
		}
	}
	return results
}

func (l *lane) processStream(f wire.Frame) error {
	c, e := wire.DecodeControl(f)
	if f.Type == wire.Data {
		return l.deliverOwned(f)
	}
	if e != nil {
		return e
	}
	switch m := c.(type) {
	case wire.Connect:
		if !l.side {
			return errors.New("OPEN_DIRECTION")
		}
		if l.stripe != nil {
			if e := l.stripe.register(f.Stream, l); e != nil {
				return e
			}
		}
		a, e := l.exit.Request(l.now(), f)
		if e != nil {
			return e
		}
		if a.Reply != nil {
			if l.stripe != nil {
				if control, decodeErr := wire.DecodeControl(*a.Reply); decodeErr == nil {
					if result, ok := control.(wire.ConnectResult); ok && result.Code != wire.ReasonNone {
						l.stripe.retire(f.Stream, l)
					}
				}
			}
			return l.SendControl(l.logicalContext(), *a.Reply)
		}
		if a.Dial != nil {
			job := *a.Dial
			l.ownerWorkers.Add(1)
			go func() {
				defer l.ownerWorkers.Done()
				r := l.exit.Dial(job)
				select {
				case l.dials <- dialDone{f.Stream, r}:
				case <-l.logicalContext().Done():
					l.exit.Complete(l.now(), r)
				}
			}()
		}
	case wire.ConnectResult:
		if l.side {
			return errors.New("OPEN_DIRECTION")
		}
		o := l.opens[f.Stream]
		if o == nil {
			return nil
		}
		fresh, e := o.protocol.Receive(1, l.now(), f)
		if e != nil {
			return e
		}
		if !fresh {
			return nil
		}
		if l.diag != nil && (m.Code != wire.ReasonNone || time.Since(o.started) > 2*time.Second) {
			l.diag.event(fmt.Sprintf("event=tcp_open_result stream=%d code=%d duration_ms=%d", f.Stream, m.Code, time.Since(o.started).Milliseconds()))
		}
		if m.Code != wire.ReasonNone {
			if l.stripe != nil {
				l.stripe.retire(f.Stream, l)
			}
			l.answer(o.request, openResult{err: fmt.Errorf("TCP_OPEN_%d", m.Code)})
			delete(l.opens, f.Stream)
			return nil
		}
		app, pump := pipes()
		app.stream = f.Stream
		l.active.Add(1)
		pump.closed = func() { l.active.Add(-1) }
		initial, addErr := l.pump.AddDeferredInitial(l.logicalContext(), f.Stream, pump)
		if addErr != nil {
			app.Close()
			if errors.Is(addErr, flow.ErrCredit) || errors.Is(addErr, session.ErrCapacity) {
				// The authenticated OPEN succeeded remotely, but local admission is full.
				// No local WINDOW/DATA was issued, so our final is exactly zero. Reject only
				// this socket and reset the peer allocation, rather than ending the owner.
				for _, early := range o.early {
					clear(early.Payload)
				}
				o.early = nil
				abort, encodeErr := wire.EncodeControl(f.Stream, wire.Abort{Final: 0, Code: wire.ReasonCapacity})
				if encodeErr != nil {
					return encodeErr
				}
				sendErr := l.SendControl(l.logicalContext(), abort)
				if l.stripe != nil {
					l.stripe.retire(f.Stream, l)
				}
				delete(l.opens, f.Stream)
				l.answer(o.request, openResult{err: errors.New("TCP_CAPACITY")})
				return sendErr
			}
			return addErr
		}
		// OPEN_OK has already authenticated this stream. Queue our initial WINDOW
		// before exposing the application socket so the peer can echo the first
		// DATA without waiting on an asynchronous Pump control drain.
		if e = l.SendControl(l.logicalContext(), initial); e != nil {
			app.Close()
			return e
		}
		for _, early := range o.early {
			if e = l.input(early); e != nil {
				app.Close()
				return e
			}
		}
		o.early = nil
		delete(l.opens, f.Stream)
		l.answer(o.request, openResult{socket: app})

	default:
		return l.deliverOwned(f)
	}
	return nil
}

func (l *lane) handleOpenRequest(req request) error {
	if err := req.ctx.Err(); err != nil {
		if l.stripe != nil {
			l.stripe.retire(req.id, l)
		}
		l.answer(req, openResult{err: err})
		return nil
	}
	if l.logicalContext().Err() != nil {
		l.answer(req, openResult{err: errors.New("LANE_UNAVAILABLE")})
		return nil
	}
	id := req.id
	if id == 0 {
		// Legacy/direct lane tests still allocate from the lane. A striped
		// Client allocates from the LogicalSession-wide odd stream space.
		if l.nextID > maxStripeStreamID {
			l.ready.Store(false)
			l.answer(req, openResult{err: errors.New("LANE_ROTATION_REQUIRED")})
			return errors.New("LANE_ROTATION_REQUIRED")
		}
		id = l.nextID
		l.nextID += 2
	} else if id%2 != 1 || id > maxStripeStreamID {
		l.answer(req, openResult{err: errStripeStream})
		return nil
	}
	r, f, e := session.NewOpenRequest(1, id, req.target, l.now(), transportPolicy())
	if e != nil {
		return e
	}
	l.opens[id] = &opening{request: req, protocol: r, started: time.Now()}
	if e = l.SendControl(l.logicalContext(), *f); e != nil {
		return e
	}

	return nil
}
func (l *lane) handleDial(d dialDone) error {
	if l.diag != nil && l.diag.tracer != nil {
		details, _ := json.Marshal(d.result.Diagnostic())
		l.diag.trace(traceEvent{Event: "target_dial", Stream: d.id, Details: details})
	}
	action, conn, e := l.exit.Complete(l.now(), d.result)
	if e != nil {
		return e
	}
	if conn == nil && l.stripe != nil {
		l.stripe.retire(d.id, l)
	}
	var initial wire.Frame
	if conn != nil {
		if action.Reply == nil {
			return errors.New("OPEN_REPLY_MISSING")
		}
		socket := l.traffic.wrap(conn)
		initial, e = l.pump.AddDeferredInitial(l.logicalContext(), d.id, socket)
		if e != nil {
			_ = socket.Close()
			if errors.Is(e, flow.ErrCredit) || errors.Is(e, session.ErrCapacity) {
				if retireErr := l.exit.Retire(d.id); retireErr != nil {
					return retireErr
				}
				if l.stripe != nil {
					l.stripe.retire(d.id, l)
				}
				reject, encodeErr := wire.EncodeControl(d.id, wire.ConnectResult{Code: wire.ReasonCapacity})
				if encodeErr != nil {
					return encodeErr
				}
				return l.SendControl(l.logicalContext(), reject)
			}
			return e
		}
	}
	if action.Reply != nil {
		// Preserve protocol activation order. OPEN_OK is queued first; the
		// initial WINDOW follows immediately and can share the same urgent
		// physical control batch without ever preceding stream acceptance.
		if e = l.SendControl(l.logicalContext(), *action.Reply); e != nil {
			return e
		}
	}
	if conn != nil {
		if e = l.SendControl(l.logicalContext(), initial); e != nil {
			return e
		}
	}

	return nil
}
func (l *lane) pollOpens() error {
	policy := transportPolicy()
	for id, o := range l.opens {
		if _, done := o.protocol.Result(); done {
			continue
		}
		if o.request.ctx.Err() != nil {
			o.abandoned = true
		}
		if o.abandoned {
			// Stop retransmitting an OPEN whose caller has gone away, but keep
			// the transaction long enough to consume a late OPEN_OK and close
			// the resulting socket cleanly. Bound that state by the normal OPEN
			// deadline even if the peer never answers.
			if time.Since(o.started) >= policy.Timeout {
				if l.stripe != nil {
					l.stripe.retire(id, l)
				}
				delete(l.opens, id)
			}
			continue
		}

		f, e := o.protocol.Poll(1, l.now())
		if errors.Is(e, session.ErrDeadline) {
			l.answer(o.request, openResult{err: fmt.Errorf("TCP_OPEN_TIMEOUT: %w", e)})
			if l.stripe != nil {
				l.stripe.retire(id, l)
			}
			delete(l.opens, id)
			continue
		}
		if e != nil {
			return e
		}
		if f != nil {
			if e = l.SendControl(l.logicalContext(), *f); e != nil {
				return e
			}
		}
	}

	return nil
}

// One actor and one existing 4 MiB / 64 socket / 4096 metadata budget per
// configured account and logical session. Carriers can reconnect independently.
func (l *lane) startOwnerActor() {
	if l.ownerStarted {
		return
	}
	l.ownerStarted = true
	close(l.actorReady)
	go func() {
		var err error
		defer func() {
			if err != nil && !errors.Is(err, context.Canceled) && l.diag != nil {
				l.diag.event("event=logical_owner_exit origin=" + errorOrigin(err) + " code=" + errorCode(err))
			}
			l.ownerCancel()
			l.ownerWorkers.Wait()
			l.pump.Close()
			if l.exit != nil {
				l.exit.Stop()
			}
			for _, o := range l.opens {
				l.answer(o.request, openResult{err: context.Canceled})
			}
			l.stripe.releaseOwner(l)
		}()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-l.ownerCtx.Done():
				return
			case <-l.pump.Done():
				err = fmt.Errorf("pump: %w", l.pump.Wait())
				return
			case f := <-l.ownerFrames:
				err = l.processStream(f)
			case req := <-l.requests:
				err = l.handleOpenRequest(req)
				if err != nil {
					err = fmt.Errorf("tcp_open: %w", err)
				}
			case d := <-l.dials:
				err = l.handleDial(d)
				if err != nil {
					err = fmt.Errorf("tcp_dial_complete: %w", err)
				}
			case <-ticker.C:
				err = l.pollOpens()
				if l.stripe.progressAge(time.Now()) > logicalProgressLimit {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}
