package mailstream

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	imap "github.com/emersion/go-imap/v2"
)

// A lost cellular connection during TLS handshake is NOT evidence of invalid
// credentials. Only actual certificate/protocol rejection must terminate
// account startup. All remaining handshake errors re-enter the existing
// staggered, capped reconnect schedule; certificate verification is never
// weakened or skipped.
func classifyTLSHandshakeError(err error) error {
	if err == nil {
		return nil
	}
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	var roots x509.SystemRootsError
	var header tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &verification) ||
		errors.As(err, &authority) ||
		errors.As(err, &hostname) ||
		errors.As(err, &certificate) ||
		errors.As(err, &roots) ||
		errors.As(err, &header) {
		return ErrAuth
	}
	if errors.As(err, &alert) {
		// RFC 8446: close_notify (0) and user_canceled (90) are
		// intentional connection endings, not proof of bad credentials.
		// A peer internal_error (80) is also not an identity rejection.
		// Retry only these three through the existing bounded schedule;
		// certificate, policy and protocol alerts remain permanent.
		switch alert {
		case tls.AlertError(0), tls.AlertError(80), tls.AlertError(90):
			return ErrClosed
		default:
			return ErrAuth
		}
	}
	return ErrClosed
}

// LOGIN tagged NO/BAD means an actual provider denial, except for the
// explicit UNAVAILABLE temporary condition. EOF/timeout/RESET and ambiguous
// command failure are transport failures, so startup must retry rather than
// permanently disabling an otherwise valid physical account.
func classifyIMAPLoginError(err error) error {
	if err == nil {
		return nil
	}
	var status *imap.Error
	if errors.As(err, &status) {
		// RFC 9051 response codes distinguish temporary provider/service
		// faults from actual invalid credentials. A saturated/busy mobile
		// provider is not evidence of an authentication failure; let the
		// existing capped, staggered startup schedule retry it instead
		// of disabling a legitimate account permanently.
		switch status.Code {
		case imap.ResponseCodeUnavailable, imap.ResponseCodeInUse,
			imap.ResponseCodeLimit, imap.ResponseCodeServerBug:
			return ErrClosed
		}
		if status.Type == imap.StatusResponseTypeNo ||
			status.Type == imap.StatusResponseTypeBad {
			return ErrAuth
		}
	}
	return ErrClosed
}
