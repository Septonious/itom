//go:build linux

package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// The actual native New/Attach/SOCKS/TUN entry points run in a child process.
// Server startup is the actual new ServeManaged. Only the public destination
// address is mapped to a local verified HTTPS origin; the encrypted transport,
// IMAP sockets, protected dial path and application copies are production code.
func TestNativeVPNProcessUsesReplacement(t *testing.T) {
	root := t.TempDir()
	// Compilation is not part of the connection's runtime deadline. A cold
	// toolchain cache must not turn build time into a fake startup timeout.
	binary := filepath.Join(root, "native.test")
	buildCtx, stopBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stopBuild()
	build := exec.CommandContext(buildCtx, "go", "test", "-c", "-o", binary, ".")
	build.Dir = filepath.Join("..", "..", "native")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native compile: %v\n%s", err, output)
	}
	mailCert, mailPEM := fixtureCertificate(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	trust := filepath.Join(root, "imap.pem")
	if err := os.WriteFile(trust, mailPEM, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", trust)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{mailCert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	backend := imapmemserver.New()
	backend.AddUser(imapmemserver.NewUser("native-fixture", "fixture-password"))
	var connections, peak atomic.Int64
	provider := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		n := connections.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if n > 10 {
			connections.Add(-1)
			return nil, nil, errors.New("fixture provider ten-connection limit")
		}
		return &countedProviderSession{Session: backend.NewSession(), count: &connections}, nil, nil
	}})
	providerDone := make(chan error, 1)
	go func() { providerDone <- provider.Serve(listener) }()
	defer func() { provider.Close(); <-providerDone }()
	httpsCert, httpsPEM := fixtureCertificate(t, []string{"example.com"}, nil)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			http.Error(w, "fixture duplex unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = io.Copy(w, r.Body)
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{httpsCert}, MinVersion: tls.VersionTLS12}
	origin.StartTLS()
	defer origin.Close()
	httpsTrust := filepath.Join(root, "https.pem")
	if err = os.WriteFile(httpsTrust, httpsPEM, 0600); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	u := servercfg.User{ID: "12345678", Name: "native fixture", Key: hex.EncodeToString(key), Up: "itom-native-up", Down: "itom-native-down", Enabled: true}
	other := u
	other.ID, other.Name, other.Up, other.Down = "87654321", "other device", "itom-other-up", "itom-other-down"
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	other.Key = hex.EncodeToString(key)
	account := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: "native-fixture", Password: "fixture-password"}}
	cfg := servercfg.Config{Schema: 1, Server: wire.ID{1}, Provider: "fixture", Accounts: []servercfg.Account{account}, Users: []servercfg.User{u, other}}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	serverJoined := false
	go func() {
		serverDone <- ServeManaged(ctx, cfg, filepath.Join(root, "server"), nil, func(ctx context.Context, _ servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
			if target.Host != "203.0.113.80" || target.Port != 443 {
				return nil, true, errors.New("FIXTURE_TARGET_DENIED")
			}
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
			if err != nil {
				return nil, true, err
			}
			return conn.(*net.TCPConn), true, nil
		})
	}()
	defer func() {
		if serverJoined {
			return
		}
		cancel()
		select {
		case <-serverDone:
		case <-time.After(3 * time.Second):
			t.Error("replacement server did not join")
		}
	}()
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socksPort := portListener.Addr().(*net.TCPAddr).Port
	portListener.Close()
	profile := servercfg.Profile{Schema: 1, Kind: "itom-android", Server: cfg.Server, Provider: cfg.Provider, Accounts: cfg.Accounts, User: u, Transport: cfg.TransportForUsers()}
	runtimeConfig := map[string]any{"Profile": profile, "DNS": []string{"127.0.0.1"}, "Socks": map[string]any{"Port": socksPort, "Authentication": false}, "Diagnostics": true}
	raw, _ := json.Marshal(runtimeConfig)
	configPath := filepath.Join(root, "native.json")
	if err = os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, binary, "-test.run=^TestRealNativeClientProcessHelper$", "-test.v", "-test.timeout=30s")
	child.Env = append(os.Environ(), "ITOM_NATIVE_TEST_CONFIG="+configPath, "ITOM_NATIVE_TEST_STATE="+filepath.Join(root, "phone"), "ITOM_NATIVE_TEST_HTTPS_ROOT="+httpsTrust, "ITOM_NATIVE_TEST_EVIDENCE="+filepath.Join(root, "native-evidence.json"), "ITOM_NATIVE_TEST_STOP="+filepath.Join(root, "native-stop"), "SSL_CERT_FILE="+trust)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("actual native SOCKS/TUN child: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
	if _, err = os.Stat(filepath.Join(root, "native-stop")); err != nil {
		t.Fatal("native helper did not complete application paths", err)
	}
	trace, err := os.ReadFile(filepath.Join(root, "server", "transport-trace.jsonl"))
	if err != nil || len(trace) == 0 {
		t.Fatal("server omitted initial trace", err)
	}
	// The first identity still owns its admitted DATA pair until authenticated
	// replacement or lease expiry. Another identity must receive BUSY without
	// opening a DATA socket; replacing the admitted identity must still work.
	profile.Kind, profile.User = "itom-imap-probe", other
	busy, err := StartClient(ctx, profile, filepath.Join(root, "other-phone"), &net.Dialer{})
	if busy != nil {
		busy.Close()
		t.Fatal("over-budget second identity was admitted")
	}
	if err == nil || err.Error() != "MAILSTREAM_SERVER_BUSY" {
		t.Fatal("expected authenticated capacity refusal", err)
	}
	profile.User = u
	replacement, err := StartClient(ctx, profile, filepath.Join(root, "replacement-phone"), &net.Dialer{})
	if err != nil {
		t.Fatal("same identity could not replace its session", err)
	}
	replacement.Close()
	cancel()
	select {
	case <-serverDone:
		serverJoined = true
	case <-time.After(3 * time.Second):
		t.Fatal("server did not join after application checks")
	}
	traffic, err := ReadTraffic(filepath.Join(root, "server", TrafficFile))
	if err != nil || traffic.Running || len(traffic.Users) != 2 || traffic.Users[0].Upload < 128<<10 || traffic.Users[0].Download < 128<<10 {
		t.Fatalf("actual exit counters missing or stale: %+v error=%v", traffic, err)
	}
	if peak.Load() > 10 {
		t.Fatal("exceeded provider connection limit", peak.Load())
	}
	t.Logf("two configured identities, provider peak=%d/10; second user BUSY; same-user restart and actual traffic accounting passed", peak.Load())
	t.Log("new runtime reached native protected sockets, authenticated DATA readiness, SOCKS HTTPS and IPv4 TUN HTTPS")
}

type countedProviderSession struct {
	imapserver.Session
	count *atomic.Int64
}

func (s *countedProviderSession) Close() error {
	s.count.Add(-1)
	return s.Session.Close()
}

func fixtureCertificate(t *testing.T, names []string, ips []net.IP) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spec := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "fixture-" + strconv.Itoa(len(names))}, DNSNames: names, IPAddresses: ips, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
