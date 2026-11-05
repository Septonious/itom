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
	"strings"
	"sync"
	"sync/atomic"
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
	metadata           map[string]*sync.RWMutex
	seeded             map[string]bool
}
type session struct {
	imapserver.Session
	counts               *counts
	user                 string
	bulkDelay            time.Duration
	prefill              int
	earlyExpunge         bool
	metadataDelay        time.Duration
	historyFolders       int
	dataFetchDelay       time.Duration
	selectedFolder       string
	appendService        time.Duration
	appendOrdinal        uint64
	rejectSelectedStatus bool
	mutated              bool
	existingCreateStall  time.Duration
	selectRefusals       int
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

// This optional fixture models account-wide interference from folder work.
// It is a stress hypothesis, not a claim about the provider's implementation.
func (s *session) accountWork(metadata bool) func() {
	if s.metadataDelay == 0 {
		return func() {}
	}
	s.counts.Lock()
	m := s.counts.metadata[s.user]
	if m == nil {
		m = &sync.RWMutex{}
		s.counts.metadata[s.user] = m
	}
	s.counts.Unlock()
	if metadata {
		m.Lock()
		time.Sleep(s.metadataDelay)
		return m.Unlock
	}
	m.RLock()
	return m.RUnlock
}
func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, o *imap.ListOptions) error {
	s.count("LIST")
	defer s.accountWork(true)()
	return s.Session.List(w, ref, patterns, o)
}
func (s *session) Append(folder string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	defer s.accountWork(false)()
	s.count("APPEND")
	if strings.Contains(folder, "-control-") {
		s.count("APPEND_CONTROL")
	} else {
		s.count("APPEND_DATA")
	}
	if s.appendService > 0 {
		s.appendOrdinal++
		// Fixed metadata/service cost, distinct from low NOOP/network RTT.
		// Independent account/command phases prevent synchronized ACK trains.
		phase := int(s.appendOrdinal+uint64(len(s.user))+uint64(s.user[len(s.user)-1]))%5 - 2
		time.Sleep(s.appendService + time.Duration(phase)*s.appendService/6)
	}
	// Provider work is distinct from TCP RTT. Serial DATA publication can
	// remain slow even while NOOP/control is fast, as in RC7 field uploads.
	if !strings.Contains(folder, "-control-") && s.bulkDelay > 0 {
		time.Sleep(s.bulkDelay)
	}
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
func (s *session) affectedControl(folder string) bool {
	return strings.Contains(folder, "-control-") && (s.user == "a02" || s.user == "a03" || s.user == "a05" || s.user == "a10")
}
func (s *session) seedExistingControls(folder string) {
	if s.existingCreateStall == 0 || !s.affectedControl(folder) {
		return
	}
	s.counts.Lock()
	defer s.counts.Unlock()
	key := "existing/" + s.user
	if s.counts.seeded[key] {
		return
	}
	s.counts.seeded[key] = true
	prefix := strings.Split(folder, "-control-")[0] + "-control-v7-"
	_ = s.Session.Create(prefix+"c2s", nil)
	_ = s.Session.Create(prefix+"s2c", nil)
}
func (s *session) Select(folder string, o *imap.SelectOptions) (*imap.SelectData, error) {
	s.seedExistingControls(folder)
	if s.selectRefusals > 0 && s.affectedControl(folder) {
		s.counts.Lock()
		key := "REFUSED_SELECT/" + s.user + "/" + folder
		refuse := s.counts.commands[key] < uint64(s.selectRefusals)
		if refuse {
			s.counts.commands[key]++
		}
		s.counts.Unlock()
		if refuse {
			return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable, Text: "temporary fixture mailbox unavailable"}
		}
	}
	defer s.accountWork(true)()
	s.count("SELECT")
	data, err := s.Session.Select(folder, o)
	if err == nil {
		s.selectedFolder = folder
	}
	return data, err
}
func (s *session) Create(folder string, o *imap.CreateOptions) error {
	s.count("CREATE")
	s.seedExistingControls(folder)
	if s.existingCreateStall > 0 && s.affectedControl(folder) {
		if _, err := s.Session.Status(folder, &imap.StatusOptions{UIDNext: true}); err == nil {
			s.count("STALLED_EXISTING_CONTROL_CREATE")
			time.Sleep(s.existingCreateStall)
		}
	}
	err := s.Session.Create(folder, o)
	if strings.Contains(folder, "-control-") && s.historyFolders > 0 {
		s.counts.Lock()
		seed := !s.counts.seeded[s.user]
		s.counts.seeded[s.user] = true
		s.counts.Unlock()
		if seed {
			prefix := strings.Split(folder, "-control-")[0]
			for i := 0; i < s.historyFolders; i++ {
				name := fmt.Sprintf("%s-%032x-c2s-00000000", prefix, i+1)
				if e := s.Session.Create(name, nil); e != nil {
					return e
				}
				if _, e := s.Session.Append(name, strings.NewReader("Subject: old fixture\r\n\r\nretained"), &imap.AppendOptions{}); e != nil {
					return e
				}
			}
		}
	}
	if err == nil && s.prefill > 0 && strings.Contains(folder, "-control-") {
		raw := []byte("MIME-Version: 1.0\r\nContent-Transfer-Encoding: base64\r\n\r\nb2xkIGNvbnRyb2wgZml4dHVyZQ==\r\n")
		for i := 0; i < s.prefill; i++ {
			if _, err = s.Session.Append(folder, strings.NewReader(string(raw)), &imap.AppendOptions{}); err != nil {
				return err
			}
		}
	}
	return err
}
func (s *session) Delete(folder string) error { s.count("DELETE"); return s.Session.Delete(folder) }
func (s *session) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	defer s.accountWork(false)()
	s.count("FETCH")
	if o != nil && len(o.BodySection) > 0 && !strings.Contains(s.selectedFolder, "-control-") {
		if seq, ok := set.(imap.SeqSet); ok {
			var rows uint64
			for _, r := range seq {
				if r.Start > 0 && r.Stop >= r.Start {
					rows += uint64(r.Stop - r.Start + 1)
				}
			}
			s.counts.Lock()
			s.counts.commands["MAX_DATA_FETCH_ROWS"] = max(s.counts.commands["MAX_DATA_FETCH_ROWS"], rows)
			s.counts.Unlock()
		}
	}
	if s.dataFetchDelay > 0 && o != nil && len(o.BodySection) > 0 && !strings.Contains(s.selectedFolder, "-control-") {
		s.count("DELAYED_DATA_FETCH")
		time.Sleep(s.dataFetchDelay)
	}
	return s.Session.Fetch(w, set, o)
}
func (s *session) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	defer s.accountWork(true)()
	s.count("STORE")
	err := s.Session.Store(w, set, f, o)
	if err == nil && s.earlyExpunge && f.Op == imap.StoreFlagsAdd {
		for _, flag := range f.Flags {
			if flag == imap.FlagDeleted {
				s.count("EARLY_EXPUNGE")
				return s.Session.Expunge(&imapserver.ExpungeWriter{}, nil)
			}
		}
	}
	return err
}
func (s *session) Status(folder string, o *imap.StatusOptions) (*imap.StatusData, error) {
	defer s.accountWork(true)()
	s.count("STATUS")
	if s.rejectSelectedStatus && s.mutated && s.selectedFolder == folder {
		s.count("REJECTED_SELECTED_STATUS")
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "post-mutation selected STATUS unsupported in fixture"}
	}
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
	defer s.accountWork(true)()
	s.count("EXPUNGE")
	err := s.Session.Expunge(w, set)
	if err == nil {
		s.mutated = true
	}
	return err
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
	clientUpload := flag.Int64("client-upload-mbps", 0, "independent shared uplink rate; requires downlink rate")
	clientDownload := flag.Int64("client-download-mbps", 0, "independent shared downlink rate; requires uplink rate")
	existingStall := flag.Duration("existing-create-stall", 0, "existing control CREATE stall on accounts a02/a03/a05/a10; SELECT/STATUS remain responsive")
	selectRefusals := flag.Int("startup-select-refusals", 0, "temporary control SELECT refusals per affected folder, shared across connections")
	appendService := flag.Duration("append-service", 0, "all APPEND per-command provider cost, with independent bounded jitter")
	rejectSelected := flag.Bool("reject-selected-status", false, "reject selected STATUS after EXPUNGE, while SELECT and other account operations work")
	clientMbps := flag.Int64("client-mbps", 500, "shared client rate across sockets and both directions")
	uploadFetchDelay := flag.Duration("upload-fetch-delay", 0, "server DATA-body FETCH service delay; NOOP/control and client download reads remain independent")
	bulkDelay := flag.Duration("bulk-publish-delay", 0, "per DATA APPEND provider service time; control remains independent")
	prefill := flag.Int("prefill-control", 0, "historical messages in each new control folder")
	metadataDelay := flag.Duration("account-metadata-delay", 0, "account-wide metadata service cost; blocks concurrent data on that account")
	historyFolders := flag.Int("history-folders", 0, "old retained DATA folders per account")
	early := flag.Bool("early-store-expunge", false, "provider emits EXPUNGE during UID STORE")
	flag.Parse()
	if *appendService < 0 || *appendService > time.Second || *selectRefusals < 0 || *selectRefusals > 8 || *existingStall < 0 || *existingStall > 30*time.Second || *clientUpload < 0 || *clientDownload < 0 || (*clientUpload == 0) != (*clientDownload == 0) || *uploadFetchDelay < 0 || *uploadFetchDelay > time.Second || *metadataDelay < 0 || *metadataDelay > time.Second || *historyFolders < 0 || *historyFolders > 1000 || *prefill < 0 || *prefill > 1000 || *bulkDelay < 0 || *bulkDelay > time.Second || *clientMbps <= 0 || *dir == "" || *accounts < 1 || *accounts > 20 {
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
	stats := &counts{metadata: map[string]*sync.RWMutex{}, seeded: map[string]bool{}, live: map[string]int{}, peak: map[string]int{}, logins: map[string]int{}, commands: map[string]uint64{}, appendSizes: map[string]uint64{}}
	var uploadReaderPort atomic.Int32
	provider := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapUIDPlus: {}, imap.CapLiteralPlus: {}, imap.CapUnselect: {}}, Logger: log.New(io.Discard, "", 0), NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		delay := time.Duration(0)
		if addr, ok := conn.NetConn().LocalAddr().(*net.TCPAddr); ok && int32(addr.Port) == uploadReaderPort.Load() {
			delay = *uploadFetchDelay
		}
		return &session{appendService: *appendService, rejectSelectedStatus: *rejectSelected, existingCreateStall: *existingStall, selectRefusals: *selectRefusals, dataFetchDelay: delay, Session: backend.NewSession(), counts: stats, bulkDelay: *bulkDelay, prefill: *prefill, earlyExpunge: *early, metadataDelay: *metadataDelay, historyFolders: *historyFolders}, nil, nil
	}})
	defer provider.Close()
	clientNet := &network{start: time.Now(), bitsPerSecond: *clientMbps * 1_000_000, step: 500 * time.Millisecond, rtts: []time.Duration{0}}
	if *clientUpload > 0 {
		clientNet.readBitsPerSecond = *clientUpload * 1_000_000
		clientNet.writeBitsPerSecond = *clientDownload * 1_000_000
	}
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
	for side, network := range []*network{clientNet, serverNet} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
		if side == 1 {
			uploadReaderPort.Store(int32(ports[side]))
		}
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
	ready := map[string]any{"client_port": ports[0], "server_port": ports[1], "root": certPath, "key": keyPath, "metrics": "http://" + control.Addr().String() + "/metrics", "accounts": *accounts, "variable": *variable, "client_upload_mbps": *clientUpload, "client_download_mbps": *clientDownload, "client_shared_mbps": *clientMbps, "server_shared_mbps": 1000}
	body, _ := json.MarshalIndent(ready, "", "  ")
	if err = os.WriteFile(filepath.Join(*dir, "provider.json"), body, 0600); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
