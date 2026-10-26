package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"itom.local/core/transport"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

type failedBootstrapDialer struct{}

func (failedBootstrapDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("private-provider-password")
}

func TestBootstrapFailuresExposeStageWithoutProviderTextOrLostCause(t *testing.T) {
	p := transport.Endpoint{Address: "127.0.0.1:1", ServerName: "fixture.local", Username: "fixture", Password: "fixture-only", Mailbox: "fast"}
	_, err := transport.DialSelected(context.Background(), failedBootstrapDialer{}, p, transport.DefaultIMAPLimits())
	if transport.IMAPStartupStage(err) != "CONNECT" || !errors.Is(err, transport.ErrUnknown) || strings.Contains(err.Error(), "private-provider") {
		t.Fatal("unsafe or missing dial diagnostic", err)
	}
	cert, roots, err := certificate()
	if err != nil {
		t.Fatal(err)
	}
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprint(c, "* OK fixture\r\n")
		r := bufio.NewReader(c)
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fmt.Fprintf(c, "%s NO private-provider-password\r\n", tag(line))
	}()
	p.Address = l.Addr().String()
	p.TLS = &tls.Config{RootCAs: roots}
	_, err = transport.DialSelected(context.Background(), &net.Dialer{}, p, transport.DefaultIMAPLimits())
	if transport.IMAPStartupStage(err) != "LOGIN" || !errors.Is(err, transport.ErrRefused) || strings.Contains(err.Error(), "private-provider") {
		t.Fatal("unsafe or missing LOGIN diagnostic", err)
	}
	<-done
}

var certificate = func() (tls.Certificate, *x509.CertPool, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return tls.Certificate{}, nil, e
	}
	spec := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture.local"}, DNSNames: []string{"fixture.local"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
	if e != nil {
		return tls.Certificate{}, nil, e
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		return tls.Certificate{}, nil, e
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots, nil
}

func TestVerifiedTLSBootstrapNegotiatesLiteralModesAndTheirSizeLimits(t *testing.T) {
	for _, mode := range []struct {
		name        string
		plus, minus bool
		size        int
	}{{"synchronizing", false, false, 4}, {"literal_plus", true, false, 4097}, {"literal_minus_small", false, true, 4}, {"literal_minus_boundary", false, true, 4096}, {"literal_minus_large", false, true, 4097}} {
		plus, minus := mode.plus, mode.minus
		payload := bytes.Repeat([]byte{'x'}, mode.size)
		nonSync := plus || minus && mode.size <= 4096
		t.Run(mode.name, func(t *testing.T) {
			cert, roots, e := certificate()
			if e != nil {
				t.Fatal(e)
			}
			listener, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				c, e := listener.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				r := bufio.NewReader(c)
				fmt.Fprint(c, "* OK healthy provider\r\n")
				line := readLine(t, r)
				if !strings.Contains(line, " LOGIN ") {
					t.Error("LOGIN missing")
				}
				fmt.Fprint(c, "BOOT1 OK\r\n")
				line = readLine(t, r)
				if !strings.Contains(line, " CAPABILITY") {
					t.Error("CAPABILITY missing")
				}
				capability := ""
				if plus {
					capability = " LITERAL+"
				}
				if minus {
					capability = " LITERAL-"
				}
				fmt.Fprintf(c, "* CAPABILITY IMAP4rev1%s\r\nBOOT2 OK\r\n", capability)
				line = readLine(t, r)
				if !strings.Contains(line, " SELECT ") {
					t.Error("SELECT missing")
				}
				fmt.Fprint(c, "* 0 EXISTS\r\n* OK [UIDVALIDITY 123] epoch\r\nBOOT3 OK\r\n")
				line = readLine(t, r)
				if nonSync {
					if !strings.Contains(line, fmt.Sprintf("{%d+}", len(payload))) {
						t.Error("negotiated LITERAL+ ignored")
					}
				} else {
					if strings.Contains(line, "+") {
						t.Error("unnegotiated LITERAL+")
					}
					fmt.Fprint(c, "+ send\r\n")
				}
				b := make([]byte, len(payload)+2)
				if _, e = io.ReadFull(r, b); e != nil {
					t.Error(e)
				}
				if !bytes.Equal(b, append(append([]byte(nil), payload...), '\r', '\n')) {
					t.Error("body corrupted")
				}
				fmt.Fprintf(c, "%s OK\r\n", tag(line))
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			selected, e := transport.DialSelected(ctx, &net.Dialer{}, transport.Endpoint{Address: listener.Addr().String(), ServerName: "fixture.local", Username: "fixture", Password: "test-only", Mailbox: "fast", TLS: &tls.Config{RootCAs: roots}}, transport.DefaultIMAPLimits())
			if e != nil {
				t.Fatal(e)
			}
			defer selected.Executor.Close()
			if selected.UIDValidity != 123 || selected.LiteralPlus != plus || selected.LiteralMinus != minus {
				t.Fatal("selection metadata lost")
			}
			cmd, e := selected.Executor.Append(ctx, "fast", payload)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = wait(t, cmd); e != nil {
				t.Fatal(e)
			}
			<-done
		})
	}
}
func TestTLSVerificationCannotBeDisabledByEndpointConfiguration(t *testing.T) {
	_, e := transport.DialSelected(context.Background(), &net.Dialer{}, transport.Endpoint{Address: "127.0.0.1:1", ServerName: "fixture.local", Username: "test", Password: "test", Mailbox: "fast", TLS: &tls.Config{InsecureSkipVerify: true}}, transport.DefaultIMAPLimits())
	if e != transport.ErrProtocol {
		t.Fatal("unsafe TLS configuration was accepted", e)
	}
}
