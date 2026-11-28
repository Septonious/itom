package mailstream

import "context"

type exitBearerKey struct{}

// WithCellularExit carries the authenticated session bearer through the
// server's individual exit dial, not global state shared between users.
func WithCellularExit(ctx context.Context, cellular bool) context.Context {
	return context.WithValue(ctx, exitBearerKey{}, cellular)
}

// CellularExit reports whether the caller selected the cellular exit path.
func CellularExit(ctx context.Context) bool {
	enabled, _ := ctx.Value(exitBearerKey{}).(bool)
	return enabled
}
