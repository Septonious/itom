package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type counts struct {
	sync.Mutex
	live, peak, logins map[string]int
	commands           map[string]uint64
	appendBytes        uint64
	appendSizes        map[string]uint64
	failures           uint64
}
type session struct {
	imapserver.Session
	counts *counts
	user   string
}

func (s *session) Login(user, password string) error {
	s.counts.Lock()
	defer s.counts.Unlock()
	if s.counts.live[user] >= 10 {
		s.counts.failures++
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "fixture session limit"}
	}
	if err := s.Session.Login(user, password); err != nil {
		return err
	}
	s.user = user
	s.counts.live[user]++
	s.counts.logins[user]++
	s.counts.peak[user] = max(s.counts.peak[user], s.counts.live[user])
	return nil
}
func (s *session) Close() error {
	if s.user != "" {
		s.counts.Lock()
		s.counts.live[s.user]--
		s.counts.Unlock()
	}
	return s.Session.Close()
}
func (s *session) count(command string) {
	s.counts.Lock()
	s.counts.commands[command]++
	s.counts.Unlock()
}
func (s *session) Append(folder string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	s.count("APPEND")
	size := r.Size()
	data, err := s.Session.Append(folder, r, o)
	if err == nil {
		bucket := "over_64k"
		for _, limit := range []int64{1024, 4096, 16384, 65536} {
			if size <= limit {
				bucket = fmt.Sprintf("at_most_%d", limit)
				break
			}
		}
		s.counts.Lock()
		s.counts.appendBytes += uint64(size)
		s.counts.appendSizes[bucket]++
		s.counts.Unlock()
	}
	return data, err
}
func (s *session) Select(folder string, o *imap.SelectOptions) (*imap.SelectData, error) {
	s.count("SELECT")
	return s.Session.Select(folder, o)
}
func (s *session) Create(folder string, o *imap.CreateOptions) error {
	s.count("CREATE")
	return s.Session.Create(folder, o)
}
func (s *session) Delete(folder string) error { s.count("DELETE"); return s.Session.Delete(folder) }
func (s *session) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	s.count("FETCH")
	return s.Session.Fetch(w, set, o)
}
func (s *session) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	s.count("STORE")
	return s.Session.Store(w, set, f, o)
}
func (s *session) Status(folder string, o *imap.StatusOptions) (*imap.StatusData, error) {
	s.count("STATUS")
	return s.Session.Status(folder, o)
}
func (s *session) Poll(w *imapserver.UpdateWriter, expunge bool) error {
	s.count("POLL")
	return s.Session.Poll(w, expunge)
}
func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	s.count("IDLE")
	return s.Session.Idle(w, stop)
}
func (s *session) Expunge(w *imapserver.ExpungeWriter, set *imap.UIDSet) error {
	s.count("EXPUNGE")
	return s.Session.Expunge(w, set)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("dir", "", "private fixture directory")
	variable := flag.Bool("variable", true, "10-50 ms client, 20-40 ms server RTT")
	accounts := flag.Int("accounts", 10, "physical accounts")
	worst := flag.Bool("worst", false, "constant 50 ms client / 40 ms server RTT")
	clientMbps := flag.Int64("client-mbps", 500, "shared client rate across sockets and both directions")
	flag.Parse()
	if *clientMbps <= 0 || *dir == "" || *accounts < 1 || *accounts > 20 {
		return fmt.Errorf("invalid fixture arguments")
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	spec := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "ITOM performance fixture"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"fixture.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPath, keyPath := filepath.Join(*dir, "fixture.pem"), filepath.Join(*dir, "fixture-key.pem")
	if err = os.WriteFile(certPath, certPEM, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	backend := imapmemserver.New()
	for n := 0; n < *accounts; n++ {
		backend.AddUser(imapmemserver.NewUser(fmt.Sprintf("a%02d", n+1), "fixture-only"))
	}
	stats := &counts{live: map[string]int{}, peak: map[string]int{}, logins: map[string]int{}, commands: map[string]uint64{}, appendSizes: map[string]uint64{}}
	provider := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapUIDPlus: {}, imap.CapLiteralPlus: {}, imap.CapUnselect: {}}, Logger: log.New(io.Discard, "", 0), NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return &session{Session: backend.NewSession(), counts: stats}, nil, nil
	}})
	defer provider.Close()
	clientNet := &network{start: time.Now(), bitsPerSecond: *clientMbps * 1_000_000, step: 500 * time.Millisecond, rtts: []time.Duration{0}}
	serverNet := &network{start: clientNet.start, bitsPerSecond: 1_000_000_000, step: 700 * time.Millisecond, rtts: []time.Duration{0}}
	if *variable {
		clientNet.rtts = []time.Duration{10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 35 * time.Millisecond}
		serverNet.rtts = []time.Duration{40 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond, 25 * time.Millisecond}
	}
	if *worst {
		clientNet.rtts = []time.Duration{50 * time.Millisecond}
		serverNet.rtts = []time.Duration{40 * time.Millisecond}
	}
	ports := []int{}
	for _, network := range []*network{clientNet, serverNet} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
		tlsListener := tls.NewListener(shapedListener{listener, ctx, network}, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		go func() { _ = provider.Serve(tlsListener) }()
	}
	control, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/phase", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		clientNet.mu.Lock()
		clientNet.start = now
		clientNet.mu.Unlock()
		serverNet.mu.Lock()
		serverNet.start = now
		serverNet.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		stats.Lock()
		defer stats.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"live": stats.live, "peak": stats.peak, "commands": stats.commands, "successful_logins": stats.logins, "appended_body_bytes": stats.appendBytes, "append_sizes": stats.appendSizes, "connection_rejections": stats.failures, "client_wire_bytes": clientNet.bytes.Load(), "server_wire_bytes": serverNet.bytes.Load(), "client_peak_queued_bytes": clientNet.peak.Load(), "server_peak_queued_bytes": serverNet.peak.Load()})
	})
	web := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	defer web.Close()
	go web.Serve(control)
	ready := map[string]any{"client_port": ports[0], "server_port": ports[1], "root": certPath, "key": keyPath, "metrics": "http://" + control.Addr().String() + "/metrics", "accounts": *accounts, "variable": *variable, "client_shared_mbps": *clientMbps, "server_shared_mbps": 1000}
	body, _ := json.MarshalIndent(ready, "", "  ")
	if err = os.WriteFile(filepath.Join(*dir, "provider.json"), body, 0600); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
