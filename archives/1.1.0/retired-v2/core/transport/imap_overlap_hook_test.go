package transport

import "context"

// Test-only access to the same internal admissions used by receive/maintenance
// owners. The ordinary public fail-fast FETCH API is deliberately unchanged.
func QueuedBodyForTest(ctx context.Context, e *Executor, uid uint32) (*Command, error) {
	return e.fetchInbox(ctx, uid)
}
func QueuedBodiesForTest(ctx context.Context, e *Executor, uids ...uint32) (*Command, error) {
	return e.fetchBodies(ctx, uids)
}
func QueuedFlagsForTest(ctx context.Context, e *Executor, uid uint32) (*Command, error) {
	return e.fetchMaintenance(ctx, []uint32{uid}, true)
}
func QueuedAbsenceForTest(ctx context.Context, e *Executor, uid uint32) (*Command, error) {
	return e.fetchMaintenance(ctx, []uint32{uid}, false)
}
