package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
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
	"strconv"
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
	largeData          map[string]bool
	messageSizes       map[string]map[imap.UID]int64
	deletedUIDs        map[string]map[imap.UID]bool
}
type session struct {
	imapserver.Session
	counts                     *counts
	user                       string
	bulkDelay                  time.Duration
	prefill                    int
	earlyExpunge               bool
	metadataDelay              time.Duration
	historyFolders             int
	dataFetchDelay             time.Duration
	largeDataDelayOnly         bool
	maxExpungeBytes            int64
	oversizedExpungeDelay      time.Duration
	selectedFolder             string
	appendService              time.Duration
	mutationRowCost            time.Duration
	appendOrdinal              uint64
	rejectSelectedStatus       bool
	mutated                    bool
	existingCreateStall        time.Duration
	selectRefusals             int
	freshControlAccounts       int
	missingControlDelay        time.Duration
	unavailableControlAccounts int
	historyBytes               []uint64
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
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "itom-") && strings.Contains(pattern, "-c-") {
			s.count("CONTROL_BOOTSTRAP_LIST")
		}
	}
	for _, pattern := range patterns {
		s.seedExistingControls(pattern)
		s.seedMixedControls(pattern)
	}
	if s.controlUnavailable() {
		s.count("UNAVAILABLE_CONTROL_LIST")
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable, Text: "fixture account unavailable"}
	}
	defer s.accountWork(true)()
	return s.Session.List(w, ref, patterns, o)
}
func (s *session) Append(folder string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	defer s.accountWork(false)()
	s.count("APPEND")
	if isControlFolder(folder) {
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
	if !isControlFolder(folder) && s.bulkDelay > 0 {
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
		if s.maxExpungeBytes > 0 {
			key := s.user + "/" + folder
			if s.counts.messageSizes[key] == nil {
				s.counts.messageSizes[key] = map[imap.UID]int64{}
			}
			s.counts.messageSizes[key][data.UID] = size
		}
		if !isControlFolder(folder) && size > 65536 {
			s.counts.largeData[s.user+"/"+folder] = true
		}
		s.counts.Unlock()
	}
	return data, err
}

// The fixture supports frozen reference names as well as the current layout.
func compactControl(folder string) bool {
	return strings.HasPrefix(folder, "itom-") && len(folder) == 31 && folder[27:30] == "-c-" && (folder[30] == 'u' || folder[30] == 'd')
}
func isControlFolder(folder string) bool {
	return compactControl(folder) || strings.Contains(folder, "-control-")
}
func fixturePrefix(folder string) string {
	if compactControl(folder) {
		return folder[:27]
	}
	return strings.Split(folder, "-control-")[0]
}
func fixtureHistoryFolder(folder string, ordinal int) string {
	prefix := fixturePrefix(folder)
	if compactControl(folder) {
		var id [16]byte
		binary.BigEndian.PutUint64(id[8:], uint64(ordinal))
		return prefix + "-" + base64.RawURLEncoding.EncodeToString(id[:]) + "-u-00"
	}
	return fmt.Sprintf("%s-%032x-c2s-00000000", prefix, ordinal)
}
func (s *session) affectedControl(folder string) bool {
	return isControlFolder(folder) && (s.user == "a02" || s.user == "a03" || s.user == "a05" || s.user == "a10")
}
func (s *session) seedExistingControls(folder string) {
	if strings.HasSuffix(folder, "-c-*") {
		folder = strings.TrimSuffix(folder, "*") + "u"
	}
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
	prefix := fixturePrefix(folder)
	if compactControl(folder) {
		_ = s.Session.Create(prefix+"-c-u", nil)
		_ = s.Session.Create(prefix+"-c-d", nil)
	} else {
		_ = s.Session.Create(prefix+"-control-v7-c2s", nil)
		_ = s.Session.Create(prefix+"-control-v7-s2c", nil)
	}
}
func (s *session) Select(folder string, o *imap.SelectOptions) (*imap.SelectData, error) {
	s.seedMixedControls(folder)
	if isControlFolder(folder) && s.controlUnavailable() {
		s.count("UNAVAILABLE_CONTROL_SELECT")
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable, Text: "fixture account unavailable"}
	}
	s.stallMissingControl(folder, "SELECT")
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
	if isControlFolder(folder) && s.historyFolders > 0 {
		s.counts.Lock()
		seed := !s.counts.seeded[s.user]
		s.counts.seeded[s.user] = true
		s.counts.Unlock()
		if seed {
			for i := 0; i < s.historyFolders; i++ {
				name := fixtureHistoryFolder(folder, i+1)
				if e := s.Session.Create(name, nil); e != nil {
					return e
				}
				if len(s.historyBytes) > 0 {
					index, e := strconv.Atoi(strings.TrimPrefix(s.user, "a"))
					if e != nil || index < 1 || index > len(s.historyBytes) {
						return fmt.Errorf("fixture history account")
					}
					amount := s.historyBytes[index-1]
					if amount > 1<<20 {
						amount -= 1 << 20
					} else {
						amount = 0
					}
					part := amount / uint64(s.historyFolders)
					if i == s.historyFolders-1 {
						part += amount % uint64(s.historyFolders)
					}
					for part > 0 {
						chunk := min(part, 512<<10)
						header := "Subject: owned historical DATA\r\n\r\n"
						if chunk < uint64(len(header)) {
							header = ""
						}
						data, e := s.Session.Append(name, strings.NewReader(header+strings.Repeat("x", int(chunk)-len(header))), &imap.AppendOptions{})
						if e != nil {
							return e
						}
						s.counts.Lock()
						key := s.user + "/" + name
						if s.counts.messageSizes[key] == nil {
							s.counts.messageSizes[key] = map[imap.UID]int64{}
						}
						s.counts.messageSizes[key][data.UID] = int64(chunk)
						s.counts.commands["SEEDED_HISTORY_BYTES"] += chunk
						s.counts.Unlock()
						part -= chunk
					}
				} else if _, e := s.Session.Append(name, strings.NewReader("Subject: old fixture\r\n\r\nretained"), &imap.AppendOptions{}); e != nil {
					return e
				}
			}
		}
	}
	if err == nil && s.prefill > 0 && isControlFolder(folder) {
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
	if o != nil && len(o.BodySection) > 0 && !isControlFolder(s.selectedFolder) {
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
	if s.dataFetchDelay > 0 && o != nil && len(o.BodySection) > 0 && !isControlFolder(s.selectedFolder) {
		s.counts.Lock()
		delay := !s.largeDataDelayOnly || s.counts.largeData[s.user+"/"+s.selectedFolder]
		s.counts.Unlock()
		if delay {
			s.count("DELAYED_DATA_FETCH")
			time.Sleep(s.dataFetchDelay)
		}
	}
	return s.Session.Fetch(w, set, o)
}
func (s *session) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	defer s.accountWork(true)()
	s.count("STORE")
	if s.mutationRowCost > 0 {
		if set, ok := set.(imap.UIDSet); ok {
			if nums, ok := set.Nums(); ok {
				s.counts.Lock()
				s.counts.commands["MAX_STORE_ROWS"] = max(s.counts.commands["MAX_STORE_ROWS"], uint64(len(nums)))
				s.counts.Unlock()
				time.Sleep(s.mutationRowCost * time.Duration(len(nums)))
			}
		}
	}
	err := s.Session.Store(w, set, f, o)
	if err == nil && s.maxExpungeBytes > 0 {
		if ids, ok := set.(imap.UIDSet); ok {
			if nums, ok := ids.Nums(); ok {
				s.counts.Lock()
				key := s.user + "/" + s.selectedFolder
				if s.counts.deletedUIDs[key] == nil {
					s.counts.deletedUIDs[key] = map[imap.UID]bool{}
				}
				for _, flag := range f.Flags {
					if flag != imap.FlagDeleted {
						continue
					}
					for _, uid := range nums {
						if f.Op == imap.StoreFlagsAdd {
							s.counts.deletedUIDs[key][uid] = true
						} else if f.Op == imap.StoreFlagsDel {
							delete(s.counts.deletedUIDs[key], uid)
						}
					}
				}
				s.counts.Unlock()
			}
		}
	}
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
	s.seedMixedControls(folder)
	s.stallMissingControl(folder, "STATUS")
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
	if s.maxExpungeBytes > 0 {
		s.counts.Lock()
		key := s.user + "/" + s.selectedFolder
		var size int64
		for uid := range s.counts.deletedUIDs[key] {
			size += s.counts.messageSizes[key][uid]
		}
		s.counts.commands["MAX_EXPUNGE_BYTES"] = max(s.counts.commands["MAX_EXPUNGE_BYTES"], uint64(size))
		if size > s.maxExpungeBytes {
			s.counts.commands["REJECTED_OVERSIZED_EXPUNGE"]++
			s.counts.Unlock()
			if s.oversizedExpungeDelay > 0 {
				s.count("DELAYED_OVERSIZED_EXPUNGE")
				time.Sleep(s.oversizedExpungeDelay)
			}
			return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "fixture deletion byte budget exceeded"}
		}
		s.counts.commands["MAX_ACCEPTED_EXPUNGE_BYTES"] = max(s.counts.commands["MAX_ACCEPTED_EXPUNGE_BYTES"], uint64(size))
		s.counts.Unlock()
	}
	err := s.Session.Expunge(w, set)
	if err == nil {
		s.mutated = true
		if s.maxExpungeBytes > 0 {
			s.counts.Lock()
			clear(s.counts.deletedUIDs[s.user+"/"+s.selectedFolder])
			s.counts.Unlock()
		}
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
	mobileLatency := flag.Bool("mobile-latency", false, "independent variable 30-100 ms client IMAP path RTT")
	rateDip := flag.Bool("client-rate-dip", false, "100 to 1 to 100 Mbps shared link: dip from second 10 through 25")
	historyVector := flag.String("history-bytes", "", "generated fixture only: per-account debt used for physically seeded old DATA")
	dir := flag.String("dir", "", "private fixture directory")
	variable := flag.Bool("variable", true, "10-50 ms client, 20-40 ms server RTT")
	accounts := flag.Int("accounts", 10, "physical accounts")
	worst := flag.Bool("worst", false, "constant 50 ms client / 40 ms server RTT")
	clientUpload := flag.Int64("client-upload-mbps", 0, "independent shared uplink rate; requires downlink rate")
	clientDownload := flag.Int64("client-download-mbps", 0, "independent shared downlink rate; requires uplink rate")
	existingStall := flag.Duration("existing-create-stall", 0, "existing control CREATE stall on accounts a02/a03/a05/a10; SELECT/STATUS remain responsive")
	selectRefusals := flag.Int("startup-select-refusals", 0, "temporary control SELECT refusals per affected folder, shared across connections")
	freshControlAccounts := flag.Int("fresh-control-accounts", 0, "first N accounts start with no control folders; remaining accounts start with both")
	missingControlDelay := flag.Duration("missing-control-delay", 0, "missing control SELECT/STATUS stall; LIST and creation remain available")
	unavailableControlAccounts := flag.Int("unavailable-control-accounts", 0, "first N accounts refuse control lookup while healthy accounts continue")
	mutationRowCost := flag.Duration("mutation-row-cost", 0, "per UID STORE service time; independent of APPEND and NOOP")
	appendService := flag.Duration("append-service", 0, "all APPEND per-command provider cost, with independent bounded jitter")
	rejectSelected := flag.Bool("reject-selected-status", false, "reject selected STATUS after EXPUNGE, while SELECT and other account operations work")
	clientMbps := flag.Int64("client-mbps", 500, "shared client rate across sockets and both directions")
	uploadFetchDelay := flag.Duration("upload-fetch-delay", 0, "server DATA-body FETCH service delay; NOOP/control and client download reads remain independent")
	downloadFetchDelay := flag.Duration("download-fetch-delay", 0, "client DATA-body FETCH service delay; independent of NOOP/control and server upload reads")
	largeDataDelayOnly := flag.Bool("large-data-fetch-delay-only", false, "apply DATA FETCH delay only after a >64 KiB MIME container in that account folder; small setup remains fast")
	oversizedDelay := flag.Duration("oversized-expunge-delay", 0, "service delay before refusing an oversized mutation; models the field timeout")
	maxExpungeMiB := flag.Int64("max-expunge-mib", 0, "reject mutation batches above this MIME-byte budget; zero disables this independent stress hypothesis")
	bulkDelay := flag.Duration("bulk-publish-delay", 0, "per DATA APPEND provider service time; control remains independent")
	prefill := flag.Int("prefill-control", 0, "historical messages in each new control folder")
	metadataDelay := flag.Duration("account-metadata-delay", 0, "account-wide metadata service cost; blocks concurrent data on that account")
	historyFolders := flag.Int("history-folders", 0, "old retained DATA folders per account")
	early := flag.Bool("early-store-expunge", false, "provider emits EXPUNGE during UID STORE")
	flag.Parse()
	var historyBytes []uint64
	if *historyVector != "" {
		parts := strings.Split(*historyVector, ",")
		if len(parts) != *accounts || *historyFolders < 1 {
			return errors.New("history byte vector length/folders")
		}
		for _, part := range parts {
			v, e := strconv.ParseUint(part, 10, 64)
			if e != nil || v > 64<<20 {
				return errors.New("history byte vector")
			}
			historyBytes = append(historyBytes, v)
		}
	}
	if *oversizedDelay < 0 || *oversizedDelay > 30*time.Second || (*oversizedDelay > 0 && *maxExpungeMiB == 0) || *maxExpungeMiB < 0 || *maxExpungeMiB > 64 || *downloadFetchDelay < 0 || *downloadFetchDelay > time.Second || *mutationRowCost < 0 || *mutationRowCost > 20*time.Millisecond || *appendService < 0 || *appendService > time.Second || *selectRefusals < 0 || *selectRefusals > 8 || *existingStall < 0 || *existingStall > 30*time.Second || *clientUpload < 0 || *clientDownload < 0 || (*clientUpload == 0) != (*clientDownload == 0) || *uploadFetchDelay < 0 || *uploadFetchDelay > time.Second || *metadataDelay < 0 || *metadataDelay > time.Second || *historyFolders < 0 || *historyFolders > 1000 || *prefill < 0 || *prefill > 1000 || *bulkDelay < 0 || *bulkDelay > time.Second || *clientMbps <= 0 || *dir == "" || *accounts < 1 || *accounts > 20 {
		return fmt.Errorf("invalid fixture arguments")
	}
	if *freshControlAccounts < 0 || *freshControlAccounts > *accounts || *unavailableControlAccounts < 0 || *unavailableControlAccounts >= *accounts || *missingControlDelay < 0 || *missingControlDelay > 30*time.Second {
		return fmt.Errorf("invalid startup fixture arguments")
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
	stats := &counts{messageSizes: map[string]map[imap.UID]int64{}, deletedUIDs: map[string]map[imap.UID]bool{}, metadata: map[string]*sync.RWMutex{}, largeData: map[string]bool{}, seeded: map[string]bool{}, live: map[string]int{}, peak: map[string]int{}, logins: map[string]int{}, commands: map[string]uint64{}, appendSizes: map[string]uint64{}}
	var uploadReaderPort atomic.Int32
	provider := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapUIDPlus: {}, imap.CapLiteralPlus: {}, imap.CapUnselect: {}}, Logger: log.New(io.Discard, "", 0), NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		delay := *downloadFetchDelay
		if addr, ok := conn.NetConn().LocalAddr().(*net.TCPAddr); ok && int32(addr.Port) == uploadReaderPort.Load() {
			delay = *uploadFetchDelay
		}
		return &session{historyBytes: historyBytes, freshControlAccounts: *freshControlAccounts, missingControlDelay: *missingControlDelay, unavailableControlAccounts: *unavailableControlAccounts, oversizedExpungeDelay: *oversizedDelay, maxExpungeBytes: *maxExpungeMiB * (1 << 20), largeDataDelayOnly: *largeDataDelayOnly, mutationRowCost: *mutationRowCost, appendService: *appendService, rejectSelectedStatus: *rejectSelected, existingCreateStall: *existingStall, selectRefusals: *selectRefusals, dataFetchDelay: delay, Session: backend.NewSession(), counts: stats, bulkDelay: *bulkDelay, prefill: *prefill, earlyExpunge: *early, metadataDelay: *metadataDelay, historyFolders: *historyFolders}, nil, nil
	}})
	defer provider.Close()
	clientNet := &network{start: time.Now(), bitsPerSecond: *clientMbps * 1_000_000, step: 500 * time.Millisecond, rtts: []time.Duration{0}}
	if *rateDip {
		if *clientMbps != 100 || *clientUpload != 0 || *clientDownload != 0 { return errors.New("rate dip requires shared 100 Mbps link") }
		clientNet.dipStart, clientNet.dipDuration, clientNet.dipBitsPerSecond = clientNet.start.Add(10*time.Second), 15*time.Second, 1_000_000
	}
	if *clientUpload > 0 {
		clientNet.readBitsPerSecond = *clientUpload * 1_000_000
		clientNet.writeBitsPerSecond = *clientDownload * 1_000_000
	}
	serverNet := &network{start: clientNet.start, bitsPerSecond: 1_000_000_000, step: 700 * time.Millisecond, rtts: []time.Duration{0}}
	if *variable {
		clientNet.rtts = []time.Duration{10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 35 * time.Millisecond}
		serverNet.rtts = []time.Duration{40 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond, 25 * time.Millisecond}
	}
	if *mobileLatency {
		clientNet.rtts = []time.Duration{30*time.Millisecond, 55*time.Millisecond, 100*time.Millisecond, 70*time.Millisecond}
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

// Separate account availability and missing-name behavior from transport code.
func (s *session) controlUnavailable() bool {
	n, e := strconv.Atoi(strings.TrimPrefix(s.user, "a"))
	return e == nil && n > 0 && n <= s.unavailableControlAccounts
}
func (s *session) seedMixedControls(name string) {
	if s.freshControlAccounts == 0 {
		return
	}
	n, e := strconv.Atoi(strings.TrimPrefix(s.user, "a"))
	if e != nil || n <= s.freshControlAccounts {
		return
	}
	prefix := ""
	if compactControl(name) {
		prefix = fixturePrefix(name)
	} else if strings.HasSuffix(name, "-c-*") {
		prefix = strings.TrimSuffix(name, "-c-*")
	}
	if prefix == "" {
		return
	}
	s.counts.Lock()
	defer s.counts.Unlock()
	key := "mixed/" + s.user + "/" + prefix
	if s.counts.seeded[key] {
		return
	}
	s.counts.seeded[key] = true
	_ = s.Session.Create(prefix+"-c-u", nil)
	_ = s.Session.Create(prefix+"-c-d", nil)
}
func (s *session) stallMissingControl(folder, command string) {
	if s.missingControlDelay == 0 || !isControlFolder(folder) {
		return
	}
	if _, e := s.Session.Status(folder, &imap.StatusOptions{}); e != nil {
		s.count("MISSING_CONTROL_" + command)
		time.Sleep(s.missingControlDelay)
	}
}
