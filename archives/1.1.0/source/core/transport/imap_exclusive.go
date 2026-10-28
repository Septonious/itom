package transport

import "context"

// Called with the wire writer held. Earlier commands have finished writing;
// their tags drain through the parser. New commands cannot enter before the
// destructive command completes its own tag or the original watchdog fires.
func (e *Executor) drainCommands(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		err, pending, changed := e.closed, len(e.pending), e.space
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if pending == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.done:
			return ErrClosed
		case <-changed:
		}
	}
}
