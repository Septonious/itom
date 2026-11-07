package mailbox

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"itom.local/core/netroute"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"

	imap "github.com/emersion/go-imap/v2"
)

// ConnectionFailure contains only application-defined categories. Provider
// replies, socket addresses and credentials never become diagnostic text.
type ConnectionFailure struct {
	Stage        string `json:"stage"`
	ResponseCode string `json:"response_code,omitempty"`
	Reason       string `json:"reason"`
}

type connectionError struct {
	code   string
	detail ConnectionFailure
}

// Preserve Unknown's accounting semantics while retaining only a safe cause.
type appendUnknownError struct{ detail ConnectionFailure }
type appendNotStartedError struct{ detail ConnectionFailure }

func (e *appendNotStartedError) Error() string { return ErrNotStarted.Error() }
func (e *appendNotStartedError) Unwrap() error { return ErrNotStarted }

func (e *appendUnknownError) Error() string { return ErrUnknown.Error() }
func (e *appendUnknownError) Unwrap() error { return ErrUnknown }

// Preserve the existing error codes and retry policy. Do not retain or unwrap
// provider text: even formatting this error must remain safe for diagnostics.
func (e *connectionError) Error() string { return e.code }
func (e *connectionError) Is(target error) bool {
	return target == ErrMailboxStateChanged && e.code == ErrMailboxStateChanged.Error()
}

func isProviderMailboxStateChange(err error) bool {
	var response *imap.Error
	return errors.As(err, &response) && response.Type == imap.StatusResponseTypeNo && response.Code == imap.ResponseCodeServerBug &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(response.Text)), "mailbox uidvalidity changed")
}

func ConnectionDetail(err error) ConnectionFailure {
	var notStarted *appendNotStartedError
	if errors.As(err, &notStarted) {
		return notStarted.detail
	}
	var appendErr *appendUnknownError
	if errors.As(err, &appendErr) {
		return appendErr.detail
	}
	var e *connectionError
	if errors.As(err, &e) {
		return e.detail
	}
	return ConnectionFailure{}
}

// FailureReason never returns provider text. Keep the original operation error
// and its retry semantics; this classification is only a diagnostic field.
func FailureReason(err error) string {
	if err == nil {
		return ""
	}
	if d := ConnectionDetail(err); d.Reason != "" {
		return d.Reason
	}
	return ConnectionDetail(connectionFailed("", "operation", err)).Reason
}

func connectionFailed(code, stage string, err error) error {
	d := ConnectionFailure{Stage: stage, Reason: "other"}
	var dns *net.DNSError
	var timeout net.Error
	var untrusted x509.UnknownAuthorityError
	var name x509.HostnameError
	var invalid x509.CertificateInvalidError
	var response *imap.Error
	winReason := ""
	if runtime.GOOS == "windows" {
		winReason = windowsSocketReason(err)
	}
	switch {
	case errors.Is(err, ErrMailboxStateChanged), isProviderMailboxStateChange(err):
		d.Stage = "select"
		d.Reason = "mailbox_state_changed"
	case errors.Is(err, netroute.ErrProtect):
		d.Stage = "socket"
		d.Reason = "protect"
	case stage == "tcp" && errors.As(err, &dns):
		d.Stage = "dns"
		switch {
		case dns.IsTimeout:
			d.Reason = "timeout"
		case dns.IsNotFound:
			d.Reason = "not_found"
		case dns.IsTemporary:
			d.Reason = "temporary"
		}
	case errors.Is(err, context.Canceled):
		d.Reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, syscall.ETIMEDOUT):
		d.Reason = "timeout"
	case errors.As(err, &timeout) && timeout.Timeout():
		d.Reason = "timeout"
	case winReason != "":
		d.Reason = winReason
	case errors.Is(err, syscall.ECONNREFUSED):
		d.Reason = "refused"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		d.Reason = "unreachable"
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		d.Reason = "permission"
	case errors.As(err, &untrusted):
		d.Reason = "certificate_authority"
	case errors.As(err, &name):
		d.Reason = "certificate_name"
	case errors.As(err, &invalid):
		d.Reason = "certificate_invalid"
	case errors.Is(err, syscall.ECONNRESET):
		d.Reason = "reset"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		d.Reason = "eof"
	case errors.Is(err, net.ErrClosed):
		d.Reason = "socket_closed"
	case errors.As(err, &response):
		if response.Type == imap.StatusResponseTypeBye {
			d.Reason = "bye"
		} else if response.Type == imap.StatusResponseTypeNo {
			d.Reason = "rejected"
		} else {
			d.Reason = "protocol"
		}
	}
	if errors.As(err, &response) {
		switch response.Code {
		case imap.ResponseCodeAuthenticationFailed, imap.ResponseCodeAuthorizationFailed,
			imap.ResponseCodeExpired, imap.ResponseCodePrivacyRequired, imap.ResponseCodeUnavailable,
			imap.ResponseCodeLimit, imap.ResponseCodeTooMany, imap.ResponseCodeContactAdmin:
			d.ResponseCode = string(response.Code)
		}
	}
	return &connectionError{code: code, detail: d}
}

// Winsock codes differ from syscall's synthetic POSIX constants on Windows.
// Values match the Windows socket API; no dependency or platform dial changes.
func windowsSocketReason(err error) string {
	var code syscall.Errno
	if !errors.As(err, &code) {
		return ""
	}
	switch code {
	case 10013:
		return "permission"
	case 10051, 10065:
		return "unreachable"
	case 10054:
		return "reset"
	case 10060:
		return "timeout"
	case 10061:
		return "refused"
	}
	return ""
}
