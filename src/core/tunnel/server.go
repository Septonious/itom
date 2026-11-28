package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/accountpolicy"
	"itom.local/core/relay"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	mailstream "itom.local/mailstream"
)

func Serve(ctx context.Context, c servercfg.Config, dir string, log func(string)) error {
	return ServeManaged(ctx, c, dir, log, nil)
}
func ServeManaged(ctx context.Context, c servercfg.Config, dir string, log func(string), managed func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error)) error {
	return ServeManagedWithExit(ctx, c, dir, log, managed, nil)
}

// ServeManagedWithExit routes ordinary tunneled TCP via the optional authenticated
// exit. Admin and readiness endpoints stay local; IMAP never uses this dialer.
func ServeManagedWithExit(ctx context.Context, c servercfg.Config, dir string, log func(string), managed func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error), upstream *relay.Dialer) error {
	trace, err := newTrace(dir)
	if err != nil {
		return err
	}
	trace.disabled = c.DiagnosticsMode == "off"
	trace.event("server_start")
	if err = c.Validate(); err != nil {
		return err
	}
	if len(c.Accounts) < accountpolicy.MinimumAccounts {
		return ErrInsufficientAccounts
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	traffic := map[string]*trafficCounters{}
	for _, u := range c.Users {
		if u.Enabled {
			traffic[u.ID] = &trafficCounters{}
		}
	}
	stopTraffic, err := monitorTraffic(ctx, dir, c.Users, traffic)
	if err != nil {
		return err
	}
	defer stopTraffic()
	done := make(chan error, len(c.Users))
	limits := c.TransportForUsers()
	// Each identity reserves one standing control socket at each endpoint.
	// An admitted pair adds six sockets: DATA read and two APPEND owners each.
	// Before admission neither endpoint opens a writer, so a refused identity
	// stays within the configured provider budget while receiving BUSY.
	slots := min(limits.Participants, (limits.AccountSessions-2*limits.Participants)/6,
		(limits.EndpointConnections/len(c.Accounts)-limits.Participants)/3)
	if slots < 1 {
		return errors.New("MAILSTREAM_SOCKET_BUDGET")
	}
	admission := &sessionAdmission{limit: slots, owners: map[string]int{}, grants: map[string][]string{}}
	for _, account := range c.Accounts {
		admission.accounts = append(admission.accounts, account.ID)
	}
	reclaimer := mailstream.NewReclamationCoordinator()
	active := 0
	for _, u := range c.Users {
		if !u.Enabled {
			continue
		}
		active++
		p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: c.Server, Provider: c.Provider, User: u, Accounts: c.Accounts, Transport: c.TransportForUsers()}
		go func() {
			done <- serveProfile(ctx, p, trace, traffic[u.ID], admission, reclaimer, log, managed, upstream)
		}()
	}
	if active == 0 {
		return servercfg.ErrConfig
	}
	var result error
	for range active {
		if err := <-done; err != nil {
			cancel()
			result = errors.Join(result, err)
		}
	}
	return result
}
func serveProfile(ctx context.Context, p servercfg.Profile, trace *traceWriter, traffic *trafficCounters, admission *sessionAdmission, reclaimer *mailstream.ReclamationCoordinator, log func(string), managed func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error), upstream *relay.Dialer) error {
	accounts, key, err := profileAccounts(p, &net.Dialer{Timeout: 15 * time.Second}, nil)
	if err != nil {
		return err
	}
	defer clear(key)
	for i := range accounts {
		accounts[i].Reclamation = reclaimer
	}
	identity := profileIdentity(p)
	link, err := mailstream.NewStoredIMAPLink(ctx, identity, accounts, false, filepath.Dir(trace.path))
	if err != nil {
		return err
	}
	defer link.Close()
	var current atomic.Pointer[mailstream.Session]
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, mailstream.ErrProtocol
		}
		if strings.EqualFold(host, pathHost) {
			if port != "1" {
				return nil, mailstream.ErrProtocol
			}
			local, peer := net.Pipe()
			go func() {
				defer peer.Close()
				stop := context.AfterFunc(ctx, func() { peer.Close() })
				defer stop()
				_, _ = io.Copy(peer, peer)
			}()
			return local, nil
		}
		if managed != nil {
			n, err := strconv.ParseUint(port, 10, 16)
			if err != nil {
				return nil, mailstream.ErrProtocol
			}
			if conn, handled, err := managed(ctx, p.User, wire.Connect{Host: host, Port: uint16(n)}); handled {
				if err != nil || conn == nil {
					return nil, err
				}
				return traffic.wrap(conn), nil
			}
		}
		conn, err := dialUserExit(ctx, network, address, upstream)
		if err != nil {
			return nil, err
		}
		return traffic.wrap(conn), nil
	}
	monitor, stop := context.WithCancel(ctx)
	defer stop()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monitor.Done():
				return
			case <-ticker.C:
				var session any
				var snap mailstream.Snapshot
				if s := current.Load(); s != nil {
					snap = s.Snapshot()
					session = snap
				}
				mail := link.Snapshot()
				if current.Load() != nil && !snap.Closed {
					now := time.Now()
					if admission.observeStorage(p.User.ID, mail, now) {
						if log != nil {
							log("event=storage_grant_replacement user=" + p.User.ID)
						}
					} else if admission.observeDemand(p.User.ID, mail, snap, now) {
						if log != nil {
							log("event=adaptive_grant_changed user=" + p.User.ID)
						}
					}
				}
				snapshot := map[string]any{"Event": "runtime", "Revision": TransportRevision, "User": p.User.ID, "Mail": mail, "Session": session, "EncryptedExit": upstream != nil, "ExitUploadBytes": traffic.upload.Load(), "ExitDownloadBytes": traffic.download.Load(), "SampledAtMS": time.Now().UnixMilli()}
				if upstream != nil {
					snapshot["RelayNegotiation"] = upstream.NegotiationCounts()
				}
				_ = trace.record(snapshot)
				if !trace.disabled {
					_ = writePrivate(filepath.Join(filepath.Dir(trace.path), "mailstream-"+p.User.ID+".json"), snapshot)
				}
			}
		}
	}()
	defer func() { stop(); <-joined }()
	return mailstream.ServeWithOptions(ctx, link, identity, key, dial, mailstream.ServerOptions{Admit: func() (func(), error) {
		return admission.acquirePreferred(p.User.ID, sequentialGrantPreferences(admission.accounts, link.Snapshot().Lines))
	}, InitialAccountGrant: func() []string { return admission.grant(p.User.ID) }, LiveAccountGrant: func() []string { return admission.grant(p.User.ID) }, IdleTimeout: 30 * time.Second, Connected: func(s *mailstream.Session) {
		current.Store(s)
		_ = trace.record(map[string]any{"Event": "session_ready", "Revision": TransportRevision, "User": p.User.ID, "Mail": link.Snapshot(), "SampledAtMS": time.Now().UnixMilli()})
		if log != nil {
			log("event=mailstream_session state=ready user=" + p.User.ID)
		}
	}})
}

// Admission counts active identities, not configured profiles. Replacing your
// own authenticated session reserves the same slot until its readers join.
// At the default ten sockets/account, two profiles can wait but only one may
// add its DATA pair. A refused second user gets an encrypted BUSY response.
type sessionAdmission struct {
	mu       sync.Mutex
	limit    int
	owners   map[string]int
	accounts []string
	grants   map[string][]string
	order    []string
	storage  map[string]*storageGrantWatch
	adaptive map[string]*grantDemandWatch
}

// Prefer usable storage headroom, not raw account IDs or lowest instantaneous
// RTT. The existing admission-order planner still controls exact grant sizes
// and exclusivity. Do not evict accounts from already admitted users merely
// because an instantaneous sample changes.
func preferredGrantAccounts(lines []mailstream.MailAccountSnapshot) []string {
	lines = append([]mailstream.MailAccountSnapshot(nil), lines...)
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		if (a.Failure != "") != (b.Failure != "") {
			return a.Failure == ""
		}
		// Prefer a usable standing reader over small differences in storage headroom.
		usable := func(x mailstream.MailAccountSnapshot) bool {
			return x.Ready && !x.BulkBlocked && x.BulkHeadroomBytes >= storageGrantMinimumHeadroom
		}
		if usable(a) != usable(b) {
			return usable(a)
		}
		if a.BulkHeadroomBytes != b.BulkHeadroomBytes {
			return a.BulkHeadroomBytes > b.BulkHeadroomBytes
		}
		if a.StoragePressure != b.StoragePressure {
			return !a.StoragePressure
		}
		if a.Ready != b.Ready {
			return a.Ready
		}
		return a.Account < b.Account
	})
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		ids = append(ids, line.Account)
	}
	return ids
}

func (a *sessionAdmission) acquire(identity string) (func(), error) {
	return a.acquirePreferred(identity, nil)
}

func (a *sessionAdmission) acquirePreferred(identity string, preferred []string) (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.owners[identity] == 0 && len(a.owners) >= a.limit {
		return nil, mailstream.ErrBusy
	}
	if a.owners[identity] == 0 {
		a.order = append(a.order, identity)
		if a.grants == nil {
			a.grants = make(map[string][]string)
		}
		// Rebalance treats previous entries as ordered preferences and validates
		// each against the configured pool. Existing users are processed first.
		a.grants[identity] = append([]string(nil), preferred...)
		a.grants = accountpolicy.Rebalance(a.accounts, a.order, a.grants)
		clear(a.adaptive)
	}
	a.owners[identity]++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.owners[identity]--
			if a.owners[identity] == 0 {
				delete(a.owners, identity)
				delete(a.storage, identity)
				delete(a.adaptive, identity)
				for i, u := range a.order {
					if u == identity {
						a.order = append(a.order[:i], a.order[i+1:]...)
						break
					}
				}
				a.grants = accountpolicy.Rebalance(a.accounts, a.order, a.grants)
				clear(a.adaptive)
			}
		})
	}, nil
}

func (a *sessionAdmission) grant(identity string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.grants[identity]...)
}

const TrafficFile = "traffic.json"

// These count useful bytes at the real server exit. Private readiness echo,
// retransmissions, mailbox headers and receipts are excluded.
type trafficCounters struct {
	upload, download atomic.Uint64
	sockets          atomic.Int64
}
type countedExit struct {
	net.Conn
	counters *trafficCounters
	once     sync.Once
}

func (t *trafficCounters) wrap(conn net.Conn) net.Conn {
	t.sockets.Add(1)
	return &countedExit{Conn: conn, counters: t}
}
func (c *countedExit) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.counters.download.Add(uint64(n))
	return n, err
}
func (c *countedExit) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.counters.upload.Add(uint64(n))
	return n, err
}
func (c *countedExit) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return errors.New("MAILSTREAM_HALF_CLOSE_UNAVAILABLE")
}
func (c *countedExit) Close() error {
	c.once.Do(func() { c.counters.sockets.Add(-1) })
	return c.Conn.Close()
}

func monitorTraffic(parent context.Context, dir string, users []servercfg.User, counters map[string]*trafficCounters) (func(), error) {
	ctx, cancel := context.WithCancel(parent)
	started := time.Now()
	write := func(running bool) error {
		snapshot := TrafficSnapshot{Instance: strconv.FormatInt(started.UnixNano(), 36), StartedAtMS: started.UnixMilli(), SampledAtMS: time.Now().UnixMilli(), Running: running}
		for _, user := range users {
			if t := counters[user.ID]; t != nil {
				snapshot.Users = append(snapshot.Users, UserTraffic{user.ID, t.upload.Load(), t.download.Load(), t.sockets.Load()})
			}
		}
		return writePrivate(filepath.Join(dir, TrafficFile), snapshot)
	}
	if err := write(true); err != nil {
		cancel()
		return nil, err
	}
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		defer write(false)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = write(true)
			}
		}
	}()
	return func() { cancel(); <-joined }, nil
}

type UserTraffic struct {
	ID       string `json:"uid"`
	Upload   uint64 `json:"upload_bytes"`
	Download uint64 `json:"download_bytes"`
	Sockets  int64  `json:"tcp_sockets"`
}
type TrafficSnapshot struct {
	Instance    string        `json:"instance"`
	StartedAtMS int64         `json:"started_at_ms"`
	SampledAtMS int64         `json:"sampled_at_ms"`
	Running     bool          `json:"running"`
	Users       []UserTraffic `json:"users"`
}

func ReadTraffic(path string) (TrafficSnapshot, error) {
	var result TrafficSnapshot
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &result)
	}
	return result, err
}
func writePrivate(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".mailstream-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Healthy accounts follow configured order. The storage-aware fallback remains
// available for a profile with blocked/unready accounts at admission.
func sequentialGrantPreferences(accounts []string, lines []mailstream.MailAccountSnapshot) []string {
	byID := map[string]mailstream.MailAccountSnapshot{}
	for _, l := range lines {
		byID[l.Account] = l
	}
	healthy := []string{}
	for _, id := range accounts {
		l := byID[id]
		if l.Ready && l.Failure == "" && !l.BulkBlocked && !l.StoragePressure && l.BulkHeadroomBytes >= storageGrantMinimumHeadroom {
			healthy = append(healthy, id)
		}
	}
	for _, id := range preferredGrantAccounts(lines) {
		if !slices.Contains(healthy, id) {
			healthy = append(healthy, id)
		}
	}
	return healthy
}
