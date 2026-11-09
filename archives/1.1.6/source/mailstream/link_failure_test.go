package mailstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"time"
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

// RFC 8446 close/cancel/internal alerts are not credential rejections.
// This test extends startup recovery without changing normal RTT or TLS
// certificate verification behavior.
func TestRC117TemporaryTLSAlertsRetryWithoutRelaxingSecurity(t *testing.T) {
	for _, code := range []tls.AlertError{0, 80, 90} {
		if got := classifyTLSHandshakeError(code); !errors.Is(got, ErrClosed) {
			t.Fatalf("temporary TLS alert %v disabled the mailbox: %v", code, got)
		}
	}
	for _, code := range []tls.AlertError{40, 42, 46, 48, 49, 70, 71, 120} {
		if got := classifyTLSHandshakeError(code); !errors.Is(got, ErrAuth) {
			t.Fatalf("identity/protocol TLS alert %v must not bypass rejection: %v", code, got)
		}
	}
	if got := classifyTLSHandshakeError(&tls.CertificateVerificationError{
		Err: x509.UnknownAuthorityError{},
	}); !errors.Is(got, ErrAuth) {
		t.Fatalf("invalid provider certificate unexpectedly recoverable: %v", got)
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

type radioDropDialer struct{}

func (radioDropDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	client, peer := net.Pipe()
	// A real cellular handover can invalidate TCP immediately after connect.
	// This exercises openMailPipeAttempt, not just the error classifier.
	_ = peer.Close()
	return client, nil
}

func TestRealTLSHandshakeRadioDropCanRetry(t *testing.T) {
	ctx,cancel:=context.WithTimeout(context.Background(),2*time.Second)
	defer cancel()
	_,err:=openMailPipeAttempt(ctx,ctx,MailAccount{
		Host:"imap.example.invalid",Port:993,Username:"test",Password:"test",
		Dialer:radioDropDialer{},
	})
	if !errors.Is(err,ErrClosed) {
		t.Fatalf("dropped mobile TLS transport permanently disabled a valid account: %v",err)
	}
}

func TestTLSCertificateFailureStillRejectsAccount(t *testing.T) {
	accounts,_:=applicationMailServer(t,true,false)
	a:=accounts[0]
	// Do not use the fixture's trusted CA; the test server's self-signed
	// certificate must be rejected by real TLS verification.
	a.Roots=nil
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	p,err:=openMailPipeAttempt(ctx,ctx,a)
	if p!=nil {
		p.close()
		t.Fatal("untrusted provider certificate unexpectedly accepted")
	}
	if !errors.Is(err,ErrAuth) {
		t.Fatalf("certificate rejection must remain permanent: %v",err)
	}
}


func TestRC117IMAPProviderBusyOrTemporaryLoginDenialRetries(t *testing.T) {
	for _, code := range []imap.ResponseCode{
		imap.ResponseCodeUnavailable, imap.ResponseCodeInUse,
		imap.ResponseCodeLimit, imap.ResponseCodeServerBug,
	} {
		for _, kind := range []imap.StatusResponseType{
			imap.StatusResponseTypeNo, imap.StatusResponseTypeBad,
		} {
			if got := classifyIMAPLoginError(&imap.Error{Type: kind, Code: code}); !errors.Is(got, ErrClosed) {
				t.Fatalf("temporarily unavailable provider %s/%s marked permanently invalid: %v", kind, code, got)
			}
		}
	}
	for _, code := range []imap.ResponseCode{
		imap.ResponseCodeAuthenticationFailed,
		imap.ResponseCodeAuthorizationFailed,
		imap.ResponseCodeNoPerm,
	} {
		if got := classifyIMAPLoginError(&imap.Error{Type: imap.StatusResponseTypeNo, Code: code}); !errors.Is(got, ErrAuth) {
			t.Fatalf("permanent credential/permission failure %s was retried: %v", code, got)
		}
	}
}

type rc117IntermittentTLSDialer struct{ calls int }

func (d *rc117IntermittentTLSDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.calls++
	if d.calls <= 2 {
		conn, peer := net.Pipe()
		peer.Close()
		return conn, nil
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

// A mobile radio drop must not poison valid credentials or suppress the
// next successful verified-TLS session startup on the same IMAP account.
func TestRC117TwoRadioDropsThenRealTLSLoginSucceeds(t *testing.T) {
	accounts, _ := applicationMailServer(t, false, false)
	dialer := &rc117IntermittentTLSDialer{}
	account := accounts[0]
	account.Dialer = dialer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		p, err := openMailPipeAttempt(ctx, ctx, account)
		if attempt < 2 {
			if p != nil {
				p.close()
				t.Fatal("failed radio handshake unexpectedly succeeded")
			}
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("radio failure %d was not classified as retryable: %v", attempt, err)
			}
			continue
		}
		if err != nil || p == nil {
			t.Fatalf("verified TLS/LOGIN failed after radio recovered: %v", err)
		}
		p.close()
	}
	if dialer.calls != 3 {
		t.Fatalf("expected exactly three dial attempts, got %d", dialer.calls)
	}
}
