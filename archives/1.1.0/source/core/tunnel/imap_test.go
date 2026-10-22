package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tlsMailPair(t *testing.T, ctx context.Context, folders []string) (mailbox.Transport, mailbox.Transport) {
	return tlsMailPairWithNetwork(t, ctx, folders, nil, "")
}

func tlsMailPairWithNetwork(t *testing.T, ctx context.Context, folders []string, network *providerTestNetwork, account string) (mailbox.Transport, mailbox.Transport) {
	t.Helper()
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("test", "secret"))
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapIdle: {}}
	if os.Getenv("ITOM_LOAD_BINARY_FETCH") == "1" {
		caps[imap.CapBinary] = struct{}{}
	}
	if os.Getenv("ITOM_LOAD_LITERAL_PLUS") == "1" {
		caps[imap.CapLiteralPlus] = struct{}{}
	}
	var debugWriter io.Writer
	if network != nil {
		debugWriter = network.debugWriter
	}
	server := imapserver.New(&imapserver.Options{NewSession: func(connection *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		var session imapserver.Session = mem.NewSession()
		// Opt-in test-provider behavior: simulate an IMAP backend that stores
		// a valid MIME DATA literal with extra trailing CRLF. Underlying IMAP
		// RFC822.SIZE then differs from the sender's canonical journal charge.
		if os.Getenv("ITOM_TEST_PROVIDER_EOL") == "1" {
			session = &providerTrailingEOLSession{Session: session}
		}
		if network != nil {
			session = &rev7DisconnectControlSession{Session: session, network: network, conn: connection.NetConn()}
			release, err := network.admitSession(account)
			if err != nil {
				_ = session.Close()
				return nil, nil, err
			}
			session = &limitedProviderSession{Session: session, release: release}
		}
		if network != nil && network.historySetup.Load() {
			return session, nil, nil
		}
		if network != nil && network.visibility.delay > 0 {
			session = &visibilitySession{Session: session, visibility: &network.visibility, account: account}
		}
		if network != nil && network.healthyEndpoints {
			// TCP RTT is shaped below TLS, independently per endpoint.
			// This is only provider processing, not another path RTT.
			service := time.Duration(loadEnvInt(t, "ITOM_LOAD_PROVIDER_SERVICE_MS", 2, 0, 100)) * time.Millisecond
			if os.Getenv("ITOM_FIELD_COMMAND_SERVICE") == "1" {
				return fieldCommandSession{delayedSession{Session: session, delay: service}}, nil, nil
			}
			return delayedSession{Session: session, delay: service}, nil, nil
		}
		if os.Getenv("ITOM_FIELD_PROVIDER_MODEL") == "1" {
			var replay *fieldLatencyReplay
			if network != nil {
				replay = network.fieldLatency
			}
			return fieldProviderSession{Session: session, replay: replay, account: account, connection: connection.NetConn(), receiveRole: new(atomic.Bool)}, nil, nil
		}
		if _, explicit := os.LookupEnv("ITOM_LOAD_COMMAND_DELAY_MS"); explicit {
			return delayedSession{Session: session, delay: time.Duration(loadEnvInt(t, "ITOM_LOAD_COMMAND_DELAY_MS", 0, 0, 5000)) * time.Millisecond}, nil, nil
		}
		if strings.Contains(t.Name(), "LoadedApplication") {
			delay := 80 * time.Millisecond
			if strings.Contains(t.Name(), "ProductionHarness") {
				delay = time.Duration(loadEnvInt(t, "ITOM_LOAD_COMMAND_DELAY_MS", 80, 0, 5000)) * time.Millisecond
			}
			return delayedSession{Session: session, delay: delay}, nil, nil
		}
		if strings.Contains(t.Name(), "latency") {
			return delayedSession{Session: session, delay: 15 * time.Millisecond}, nil, nil
		}
		return session, nil, nil
	}, Caps: caps, DebugWriter: debugWriter})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveListener := net.Listener(listener)
	if network != nil {
		serveListener = providerTestListener{Listener: listener, network: network, account: account}
	}
	go server.Serve(tls.NewListener(serveListener, &tls.Config{Certificates: []tls.Certificate{cert}}))
	t.Cleanup(func() { server.Close(); listener.Close() })
	allowedFolders := append([]string(nil), folders...)
	for _, base := range folders {
		for i := 0; i < 4; i++ {
			allowedFolders = append(allowedFolders, fmt.Sprintf("%s-data-%02d", base, i))
		}
	}
	cfg := mailbox.Config{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: "test", Password: "secret"}
	var clients []mailbox.Transport
	for i := 0; i < 2; i++ {
		c, err := mailbox.DialWithRoots(ctx, cfg, allowedFolders, roots)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		for _, f := range folders {
			if err = c.Ensure(ctx, f); err != nil {
				t.Fatal(err)
			}
		}
		m := &laneMail{Client: c, dial: func(ctx context.Context) (*mailbox.Client, error) {
			return mailbox.DialWithRoots(ctx, cfg, allowedFolders, roots)
		}}
		t.Cleanup(func() { m.Close() })
		clients = append(clients, m)
	}
	return clients[0], clients[1]
}

// Model a provider with nonzero command latency; localhost alone concealed the
// original per-message GC amplification and aggressive retry traffic.
type delayedSession struct {
	imapserver.Session
	delay time.Duration
}

func (s delayedSession) Select(f string, o *imap.SelectOptions) (*imap.SelectData, error) {
	time.Sleep(s.delay)
	return s.Session.Select(f, o)
}
func (s delayedSession) Append(f string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	time.Sleep(s.delay)
	return s.Session.Append(f, r, o)
}
func (s delayedSession) Search(k imapserver.NumKind, c *imap.SearchCriteria, o *imap.SearchOptions) (*imap.SearchData, error) {
	time.Sleep(s.delay)
	return s.Session.Search(k, c, o)
}
func (s delayedSession) Fetch(w *imapserver.FetchWriter, n imap.NumSet, o *imap.FetchOptions) error {
	time.Sleep(s.delay)
	return s.Session.Fetch(w, n, o)
}
func (s delayedSession) Store(w *imapserver.FetchWriter, n imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	time.Sleep(s.delay)
	return s.Session.Store(w, n, f, o)
}
func (s delayedSession) Expunge(w *imapserver.ExpungeWriter, n *imap.UIDSet) error {
	time.Sleep(s.delay)
	return s.Session.Expunge(w, n)
}

// This is a test-only provider mutation, NOT a client send optimization.
// It appends CRLF to canonical ITQ1 DATA after accepting the network literal.
// The real provider stores a longer RFC822 literal but the authenticated
// decoded DATA body remains identical. Control messages are untouched.
type providerTrailingEOLSession struct{ imapserver.Session }

func (s *providerTrailingEOLSession) Append(folder string, literal imap.LiteralReader, opts *imap.AppendOptions) (*imap.AppendData, error) {
	const limit = mailbox.MaxMIME + 1024
	original, err := io.ReadAll(io.LimitReader(literal, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(original)) != literal.Size() {
		return nil, io.ErrUnexpectedEOF
	}
	if len(original) > mailbox.MaxMIME {
		return nil, mailbox.ErrLimit
	}
	if decoded, e := mailbox.Decode(original); e == nil {
		if bytes.HasPrefix(decoded, []byte("ITQ1")) || bytes.HasPrefix(decoded, []byte(wire.DataEnvelopeMagic)) {
			original = append(original, '\r', '\n', '\r', '\n')
		}
		clear(decoded)
	}
	return s.Session.Append(folder, bytes.NewReader(original), opts)
}
