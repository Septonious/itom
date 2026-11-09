package mailstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"testing"

	imap "github.com/emersion/go-imap/v2"
)

func TestPoorMobileTLSDisconnectRetriesButVerificationRemainsStrict(t *testing.T) {
	for _, err := range []error{io.EOF,context.DeadlineExceeded,net.ErrClosed,errors.New("temporary handshake reset")} {
		if got:=classifyTLSHandshakeError(err);!errors.Is(got,ErrClosed) {
			t.Fatalf("transient TLS transport %v treated as permanent auth: %v",err,got)
		}
	}
	for _, err := range []error{
		&tls.CertificateVerificationError{Err:x509.UnknownAuthorityError{}},
		x509.HostnameError{},
		tls.RecordHeaderError{},
		tls.AlertError(42),
	} {
		if got:=classifyTLSHandshakeError(err);!errors.Is(got,ErrAuth) {
			t.Fatalf("invalid TLS identity/protocol must fail closed: %T %v",err,got)
		}
	}
}

func TestPoorMobileIMAPLoginDisconnectsRetryWithoutWeakeningAuth(t *testing.T) {
	for _, err := range []error{io.EOF,context.DeadlineExceeded,net.ErrClosed,
		&imap.Error{Type:imap.StatusResponseTypeNo,Code:imap.ResponseCodeUnavailable}} {
		if got:=classifyIMAPLoginError(err);!errors.Is(got,ErrClosed) {
			t.Fatalf("temporary provider failure must retry: %T %v",err,got)
		}
	}
	for _, typ:=range []imap.StatusResponseType{imap.StatusResponseTypeNo,imap.StatusResponseTypeBad} {
		err:=&imap.Error{Type:typ}
		if got:=classifyIMAPLoginError(err);!errors.Is(got,ErrAuth) {
			t.Fatalf("explicit server rejection must not retry credentials: %v",got)
		}
	}
}
