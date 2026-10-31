package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"itom.local/core/mailbox"
	"itom.local/core/transport"
)

func TestBothTransactionImplementationsAgainstIndependentIMAPServer(t *testing.T) {
	cfg, endpoint, roots := transactionFixture(t, false)

	for _, grouped := range []bool{false, true} {
		for _, reference := range []bool{false, true} {
			name := "session_single"
			if reference {
				name = "reference_single"
			}
			if grouped {
				name += "_batch"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var steps []transport.TransactionProbeStep
				emit := func(step transport.TransactionProbeStep) { steps = append(steps, step) }
				var err error
				if reference {
					err = probeReferenceWithDial(ctx, cfg, grouped, emit, func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
						return mailbox.DialWithRoots(ctx, cfg, folders, roots)
					})
				} else {
					err = transport.ProbeMailboxTransaction(ctx, &net.Dialer{}, endpoint, grouped, emit)
				}
				if err != nil {
					t.Fatal("transaction", err, steps)
				}
				if len(steps) == 0 || steps[len(steps)-1].Stage != "verify_absence" || steps[len(steps)-1].Outcome != "ok" {
					t.Fatal(steps)
				}
				encoded, _ := json.Marshal(steps)
				for _, secret := range []string{cfg.Password, "itom-wirecheck-", "BODY[]", "LOGIN ", "private-fixture"} {
					if strings.Contains(string(encoded), secret) {
						t.Fatal("private report detail", secret)
					}
				}
			})
		}
	}
}

// The independent server returns a deliberately private response string.
// Neither implementation may include it in the diagnostic report.
type refusingExpungeSession struct{ imapserver.Session }

func (refusingExpungeSession) Expunge(*imapserver.ExpungeWriter, *imap.UIDSet) error {
	return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "private-fixture-password provider-private-detail"}
}
func TestTransactionRefusalRetainsExactStageAndExcludesProviderText(t *testing.T) {
	cfg, endpoint, roots := transactionFixture(t, true)
	for _, reference := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var steps []transport.TransactionProbeStep
		emit := func(s transport.TransactionProbeStep) { steps = append(steps, s) }
		var err error
		if reference {
			err = probeReferenceWithDial(ctx, cfg, false, emit, func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
				return mailbox.DialWithRoots(ctx, cfg, folders, roots)
			})
		} else {
			err = transport.ProbeMailboxTransaction(ctx, &net.Dialer{}, endpoint, false, emit)
		}
		cancel()
		if err == nil || len(steps) == 0 {
			t.Fatal("refused deletion reported success", steps)
		}
		last := steps[len(steps)-1]
		if last.Outcome != "failed" || (last.Stage != "uid_expunge" && last.Stage != "verified_delete") {
			t.Fatal("lost failure phase", steps)
		}
		raw, _ := json.Marshal(steps)
		for _, secret := range []string{cfg.Password, "provider-private-detail", "itom-wirecheck-"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private diagnostic detail", secret)
			}
		}
	}
}
func transactionFixture(t *testing.T, refuse bool) (mailbox.Config, transport.Endpoint, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spec := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture.local"}, DNSNames: []string{"fixture.local"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	backend := imapmemserver.New()
	backend.AddUser(imapmemserver.NewUser("fixture", "private-fixture-password"))
	server := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		session := backend.NewSession()
		if refuse {
			session = refusingExpungeSession{session}
		}
		return session, nil, nil
	}})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	cfg := mailbox.Config{Host: "fixture.local", DialIP: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: "fixture", Password: "private-fixture-password"}
	endpoint := transport.Endpoint{Address: listener.Addr().String(), ServerName: "fixture.local", Username: cfg.Username, Password: cfg.Password, AccountID: "fixture", TLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	return cfg, endpoint, roots
}
