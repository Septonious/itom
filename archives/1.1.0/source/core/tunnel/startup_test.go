package tunnel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"itom.local/core/servercfg"
)

func TestStartupFailureReportsSafePhaseAndPreservesInspectableCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := StartClient(ctx, servercfg.Profile{}, t.TempDir(), nil)
	if !errors.Is(err, context.Canceled) || err.Error() != "V2_STARTUP_PROFILE" {
		t.Fatalf("lost startup phase or cancellation cause: %v", err)
	}
	secret := errors.New("provider reply containing a password and private path")
	wrapped := &startupError{code: "V2_STARTUP_IMAP", cause: secret}
	if !errors.Is(wrapped, secret) || strings.Contains(wrapped.Error(), "password") {
		t.Fatal("provider details leaked or cause lost")
	}
}
