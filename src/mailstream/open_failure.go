package mailstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
)

// Fixed authenticated RESET reasons. Zero retains the normal stream reset.
// No destination, credential, or raw remote error is transmitted.
type OpenFailureReason uint64

const (
	OpenFailurePolicy OpenFailureReason = iota + 1
	OpenFailureDNS
	OpenFailureConnect
	OpenFailureTimeout
	OpenFailureCanceled
	OpenFailureOther
	OpenFailureRelayDial
	OpenFailureRelayTLS
	OpenFailureRelaySetup
	OpenFailureRelayCooldown
)

var (
	ErrExitRelayDial     = errors.New("MAILSTREAM_EXIT_RELAY_DIAL")
	ErrExitRelayTLS      = errors.New("MAILSTREAM_EXIT_RELAY_TLS")
	ErrExitRelaySetup    = errors.New("MAILSTREAM_EXIT_RELAY_SETUP")
	ErrExitRelayCooldown = errors.New("MAILSTREAM_EXIT_RELAY_COOLDOWN")
)

type RemoteOpenError struct{ Reason OpenFailureReason }

func (e RemoteOpenError) Error() string { return fmt.Sprintf("MAILSTREAM_REMOTE_OPEN_%d", e.Reason) }
func (e RemoteOpenError) Unwrap() error { return ErrClosed }
func validOpenFailure(v uint64) bool {
	return v >= uint64(OpenFailurePolicy) && v <= uint64(OpenFailureRelayCooldown)
}

// Classify before generic ErrClosed/ErrAuth wrappers obscure the cause.
func ClassifyOpenFailure(err error, expired bool, ctxErr error) OpenFailureReason {
	var ne net.Error
	switch {
	case expired, errors.Is(ctxErr, context.DeadlineExceeded):
		return OpenFailureTimeout
	case errors.Is(ctxErr, context.Canceled), errors.Is(err, context.Canceled):
		return OpenFailureCanceled
	case errors.Is(err, ErrExitRelayTLS):
		return OpenFailureRelayTLS
	case errors.Is(err, ErrExitRelayDial):
		return OpenFailureRelayDial
	case errors.Is(err, ErrExitRelaySetup):
		return OpenFailureRelaySetup
	case errors.Is(err, ErrExitRelayCooldown):
		return OpenFailureRelayCooldown
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return OpenFailureTimeout
	case errors.Is(err, ErrExitPolicy):
		return OpenFailurePolicy
	case errors.Is(err, ErrExitDNS):
		return OpenFailureDNS
	case errors.Is(err, ErrExitConnect):
		return OpenFailureConnect
	default:
		return OpenFailureOther
	}
}
