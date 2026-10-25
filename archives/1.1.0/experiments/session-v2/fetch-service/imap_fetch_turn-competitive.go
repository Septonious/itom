package transport
import "context"
func (e *Executor) acquireFetchTurn(ctx context.Context) (func(*operation), error) { return func(*operation){}, nil }
func (e *Executor) releaseFetchTurnLocked(op *operation) {}
