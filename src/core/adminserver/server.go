// Package adminserver implements server-authorized, reversible administration.
package adminserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"itom.local/core/adminapi"
	"itom.local/core/mailbox"
	"itom.local/core/manager"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

type Server struct {
	State  string
	Launch func(string) error
	Now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	jobs   map[string]*job
	slots  chan struct{}
}
type job struct {
	ID, Owner, Text string
	Done            bool
	Created         time.Time
}

func New(ctx context.Context, state string) *Server {
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{State: state, Now: time.Now, ctx: ctx, cancel: cancel, jobs: map[string]*job{}, slots: make(chan struct{}, 8)}
	s.Launch = func(id string) error { return launchGuard(state, id) }
	return s
}
func (s *Server) Close()     { s.cancel() }
func (s *Server) db() string { return filepath.Join(s.State, "server.db") }
func (s *Server) authorized(user servercfg.User) (servercfg.Config, error) {
	c, e := servercfg.Load(s.db())
	if e != nil {
		return c, e
	}
	for _, u := range c.Users {
		if u.ID == user.ID && u.Enabled && u.Name == "admin" && subtle.ConstantTimeCompare([]byte(u.Key), []byte(user.Key)) == 1 {
			return c, nil
		}
	}
	return c, errors.New("ADMIN_ACCESS_DENIED")
}

// Dial is installed on each authenticated server user's logical TCP exit.
// Its ephemeral loopback pair is never a public API: only the socket whose
// exact OS-assigned source address matches our dial is admitted to the handler.
func (s *Server) Dial(ctx context.Context, user servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
	if !strings.EqualFold(target.Host, adminapi.Host) {
		return nil, false, nil
	}
	if target.Port != adminapi.Port {
		return nil, true, errors.New("ADMIN_ACCESS_DENIED")
	}
	if _, e := s.authorized(user); e != nil {
		return nil, true, e
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, true, ctx.Err()
	default:
		return nil, true, errors.New("ADMIN_BUSY")
	}
	ln, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		<-s.slots
		return nil, true, e
	}
	defer ln.Close()
	_ = ln.SetDeadline(time.Now().Add(2 * time.Second))
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp4", ln.Addr().String())
	if e != nil {
		<-s.slots
		return nil, true, e
	}
	tcp := c.(*net.TCPConn)
	for {
		peer, err := ln.AcceptTCP()
		if err != nil {
			tcp.Close()
			<-s.slots
			return nil, true, err
		}
		if peer.RemoteAddr().String() != tcp.LocalAddr().String() {
			peer.Close()
			continue
		}
		conn := &ownedConn{TCPConn: peer, done: make(chan struct{})}
		listener := &oneListener{conn: conn}
		nonce, err := adminapi.NewID()
		if err != nil {
			conn.Close()
			tcp.Close()
			<-s.slots
			return nil, true, err
		}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handle(user, nonce, w, r) })
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 4096}
		srv.SetKeepAlivesEnabled(false)
		go func() {
			defer func() { <-s.slots }()
			stop := context.AfterFunc(s.ctx, func() { srv.Close() })
			defer stop()
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(conn, "ITOM-ADMIN-1 "+nonce+"\n"); err != nil {
				conn.Close()
				return
			}
			_ = conn.SetWriteDeadline(time.Time{})
			_ = srv.Serve(listener)
			_ = srv.Close()
		}()
		return tcp, true, nil
	}
}

type ownedConn struct {
	*net.TCPConn
	once sync.Once
	done chan struct{}
}

func (c *ownedConn) Close() error {
	e := c.TCPConn.Close()
	c.once.Do(func() { close(c.done) })
	return e
}

type oneListener struct {
	mu       sync.Mutex
	conn     *ownedConn
	accepted bool
}

func (l *oneListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.accepted {
		l.accepted = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.conn.done
	return nil, net.ErrClosed
}
func (l *oneListener) Close() error   { return l.conn.Close() }
func (l *oneListener) Addr() net.Addr { return l.conn.LocalAddr() }

func (s *Server) handle(user servercfg.User, nonce string, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	result := adminapi.Response{Error: "ADMIN_REQUEST_INVALID"}
	if r.Method == http.MethodPost && r.URL.Path == "/rpc" {
		raw, e := io.ReadAll(io.LimitReader(r.Body, adminapi.MaxRequest+1))
		if e == nil {
			proof, err := adminapi.Proof(user.Key, nonce, raw)
			if err == nil && subtle.ConstantTimeCompare([]byte(proof), []byte(r.Header.Get("X-ITOM-Admin-Proof"))) == 1 {
				q, e := adminapi.DecodeRequest(raw)
				if e == nil {
					result = s.Execute(r.Context(), user, q)
				}
			} else {
				result.Error = "ADMIN_ACCESS_DENIED"
			}
		}
	}
	_ = json.NewEncoder(w).Encode(result)
}
func pending(g servercfg.Guard, now time.Time) *adminapi.Pending {
	if g.ID == "" {
		return nil
	}
	stamp := g.LastBeatMS
	if stamp == 0 {
		stamp = g.CreatedMS
	}
	p := &adminapi.Pending{ID: g.ID, Owner: g.Owner, State: g.State, LossDeadlineMS: stamp + servercfg.GuardLossTimeout.Milliseconds(), HealthySinceMS: g.HealthySinceMS, ServerTimeMS: now.UnixMilli()}
	if g.Active() {
		p.Challenge = g.Challenge
	}
	return p
}
func (s *Server) currentPending() *adminapi.Pending {
	g, e := servercfg.GuardStatus(s.db())
	if e != nil {
		return nil
	}
	return pending(g, s.Now())
}
func (s *Server) Execute(ctx context.Context, user servercfg.User, q adminapi.Request) adminapi.Response {
	c, e := s.authorized(user)
	if e != nil {
		return adminapi.Response{Error: "ADMIN_ACCESS_DENIED"}
	}
	fail := func(code string) adminapi.Response {
		return adminapi.Response{Error: code, Pending: s.currentPending()}
	}
	ok := func(text string) adminapi.Response {
		return adminapi.Response{OK: true, Text: text, Pending: s.currentPending()}
	}
	switch q.Operation {
	case "status":
		t := c.TransportForUsers()
		mode := c.DiagnosticsMode
		if mode == "" {
			mode = "on"
		}
		v := &adminapi.Status{Revision: configRevision(c), Admin: true, CurrentUID: user.ID, Provider: c.Provider, Service: serviceState(ctx), Diagnostics: mode, GlobalMessage: c.Notice.Text, WatchdogAvailable: watchdogAvailable(), Transport: adminapi.Resources{Layout: t.Layout, DataShards: t.DataShards, AccountSessions: t.AccountSessions, EndpointConnections: t.EndpointConnections, LoginParallelism: t.LoginParallelism}}
		for _, u := range c.Users {
			v.Users = append(v.Users, adminapi.User{ID: u.ID, Name: u.Name, Enabled: u.Enabled, Message: c.ClientNotices[u.ID]})
		}
		for _, a := range c.Accounts {
			v.Accounts = append(v.Accounts, adminapi.Account{ID: a.ID, Host: a.Mail.Host, DialIP: a.Mail.DialIP, Port: a.Mail.Port, TLS: a.Mail.TLS, Username: a.Mail.Username})
		}
		if traffic, e := tunnel.ReadTraffic(filepath.Join(s.State, "tunnel-server", tunnel.TrafficFile)); e == nil {
			v.Traffic, _ = json.Marshal(traffic)
		}
		return adminapi.Response{OK: true, Status: v, Pending: s.currentPending()}
	case "undo":
		g, e := servercfg.GuardStatus(s.db())
		if e != nil || g.ID != q.ID || g.Owner != user.ID || !g.Active() {
			return fail("ADMIN_CHANGE_CONFLICT")
		}
		if servercfg.MarkGuardRollback(s.db(), q.ID) != nil {
			return fail("ADMIN_CHANGE_CONFLICT")
		}
		return ok("Reverting change…")
	case "heartbeat":
		g, e := servercfg.BeatGuard(s.db(), user.ID, q.ID, q.Challenge, s.Now())
		if e != nil {
			return fail("ADMIN_HEARTBEAT_REJECTED")
		}
		return adminapi.Response{OK: true, Pending: pending(g, s.Now())}
	case "logs":
		text, e := limitedCommand(ctx, "journalctl", "--unit=itom.service", "--lines=100", "--no-pager", "--output=short-iso")
		if e != nil {
			return fail("ADMIN_LOGS_UNAVAILABLE")
		}
		return ok(text)
	case "trace":
		text, e := tailFile(filepath.Join(s.State, "tunnel-server", "transport-trace.jsonl"), 64<<10)
		if e != nil {
			return fail("ADMIN_TRACE_UNAVAILABLE")
		}
		return ok(text)
	case "profile":
		link, e := c.AndroidLink(q.UID)
		if e != nil {
			return fail("ADMIN_PROFILE_UNAVAILABLE")
		}
		return ok(link)
	case "check":
		return s.startCheck(user, c, q.AccountID)
	case "check_status":
		s.mu.Lock()
		defer s.mu.Unlock()
		j := s.jobs[q.ID]
		if j == nil || j.Owner != user.ID {
			return fail("ADMIN_CHECK_NOT_FOUND")
		}
		text := j.Text
		if !j.Done {
			text = "Checking accounts…\n" + text
		}
		return adminapi.Response{OK: true, ID: j.ID, Text: text, Total: boolInt(j.Done)}
	case "diagnostics_export":
		id, e := adminapi.NewID()
		if e != nil {
			return fail("ADMIN_EXPORT_FAILED")
		}
		dir := filepath.Join(s.State, "admin-exports")
		if os.MkdirAll(dir, 0700) != nil {
			return fail("ADMIN_EXPORT_FAILED")
		}
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if st, e := entry.Info(); e == nil && (strings.HasPrefix(entry.Name(), user.ID+"-") || s.Now().Sub(st.ModTime()) > 10*time.Minute) {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
		path := filepath.Join(dir, user.ID+"-"+id+".zip")
		if manager.ExportDiagnostics(s.State, path) != nil {
			return fail("ADMIN_EXPORT_FAILED")
		}
		st, e := os.Stat(path)
		if e != nil {
			return fail("ADMIN_EXPORT_FAILED")
		}
		return adminapi.Response{OK: true, ID: id, Total: st.Size()}
	case "diagnostics_chunk":
		if !validID(q.ID) || q.Offset < 0 || q.Offset > 64<<20 {
			return fail("ADMIN_EXPORT_INVALID")
		}
		f, e := os.Open(filepath.Join(s.State, "admin-exports", user.ID+"-"+q.ID+".zip"))
		if e != nil {
			return fail("ADMIN_EXPORT_NOT_FOUND")
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || s.Now().Sub(st.ModTime()) > 10*time.Minute || q.Offset > st.Size() {
			return fail("ADMIN_EXPORT_NOT_FOUND")
		}
		b := make([]byte, min(int64(128<<10), st.Size()-q.Offset))
		n, e := f.ReadAt(b, q.Offset)
		if e != nil && e != io.EOF {
			return fail("ADMIN_EXPORT_FAILED")
		}
		return adminapi.Response{OK: true, ID: q.ID, Data: b[:n], Total: st.Size()}
	}
	if !mutation(q.Operation) {
		return fail("ADMIN_OPERATION_UNAVAILABLE")
	}
	plan, e := json.Marshal(q)
	if e != nil || !validID(q.ID) {
		return fail("ADMIN_CHANGE_INVALID")
	}
	if prior, e := servercfg.FindGuard(s.db(), q.ID); e != nil {
		return fail("ADMIN_CHANGE_CONFLICT")
	} else if prior.ID != "" {
		g, _, e := servercfg.BeginGuard(s.db(), user, q.ID, plan, s.Now(), func(c servercfg.Config) error {
			if q.Revision != "" && q.Revision != configRevision(c) {
				return servercfg.ErrGuardConflict
			}
			return nil
		})
		if e != nil {
			return fail("ADMIN_CHANGE_CONFLICT")
		}
		if g.State == "canceled" || g.State == "rolled_back" {
			return fail("ADMIN_CHANGE_REVERTED")
		}
		return adminapi.Response{OK: true, Text: "Change already accepted.", Pending: pending(g, s.Now())}
	}
	if q.Revision != "" && q.Revision != configRevision(c) {
		return fail("ADMIN_CHANGE_CONFLICT")
	}
	// Validate a deep copy before arming anything; no trial can begin with an
	// invalid account, unavailable client or malformed transport configuration.
	raw, _ := json.Marshal(c)
	var preview servercfg.Config
	_ = json.Unmarshal(raw, &preview)
	if e = apply(&preview, q); e != nil || preview.Validate() != nil {
		return fail("ADMIN_CHANGE_INVALID")
	}
	g, fresh, e := servercfg.BeginGuard(s.db(), user, q.ID, plan, s.Now(), func(c servercfg.Config) error {
		if q.Revision != "" && q.Revision != configRevision(c) {
			return servercfg.ErrGuardConflict
		}
		return nil
	})
	if e != nil {
		if errors.Is(e, servercfg.ErrGuardPending) {
			return fail("ADMIN_CHANGE_PENDING")
		}
		return fail("ADMIN_CHANGE_CONFLICT")
	}
	if fresh {
		if e = s.Launch(q.ID); e != nil {
			_ = servercfg.CancelGuard(s.db(), q.ID)
			return fail("ADMIN_WATCHDOG_UNAVAILABLE")
		}
	}
	return adminapi.Response{OK: true, Text: "Change accepted. Automatic rollback protection is active.", Pending: pending(g, s.Now())}
}
func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func mutation(op string) bool {
	switch op {
	case "global_message", "client_message", "client_add", "client_rename", "client_enabled", "client_remove", "account_add", "account_password", "account_pin", "account_remove", "diagnostics", "transport", "service_start", "service_stop", "service_restart", "service_install":
		return true
	}
	return false
}
func apply(c *servercfg.Config, q adminapi.Request) error {
	switch q.Operation {
	case "global_message":
		if c.Notice.Revision >= wire.MaxOffset-1 {
			return servercfg.ErrConfig
		}
		return c.SetNotice(q.Text)
	case "client_message":
		if c.Notice.Revision >= wire.MaxOffset-1 {
			return servercfg.ErrConfig
		}
		return c.SetClientNotice(q.UID, q.Text)
	case "client_add":
		if len(c.RetiredUsers) >= 1024 {
			return servercfg.ErrConfig
		}
		_, e := c.AddUser(q.Name)
		return e
	case "client_remove":
		return c.RemoveUser(q.UID)
	case "client_rename", "client_enabled":
		for i := range c.Users {
			if c.Users[i].ID == q.UID {
				if q.Operation == "client_rename" {
					c.Users[i].Name = q.Name
				} else {
					if q.Enabled == nil {
						return servercfg.ErrConfig
					}
					c.Users[i].Enabled = *q.Enabled
				}
				return c.Validate()
			}
		}
		return servercfg.ErrConfig
	case "account_add":
		if len(c.Retired) >= 1024 {
			return servercfg.ErrConfig
		}
		if q.Mail == nil {
			return servercfg.ErrConfig
		}
		_, e := c.AddAccount(mailbox.Config{Host: q.Mail.Host, DialIP: q.Mail.DialIP, Port: q.Mail.Port, TLS: q.Mail.TLS, Username: q.Mail.Username, Password: q.Mail.Password})
		return e
	case "account_remove":
		return c.RemoveAccount(q.AccountID)
	case "account_pin":
		if q.AccountID == "" {
			return servercfg.ErrConfig
		}
		id := q.AccountID
		if id == "*" {
			id = ""
		}
		return c.SetAccountDialIP(id, q.DialIP)
	case "account_password":
		for i := range c.Accounts {
			if c.Accounts[i].ID == q.AccountID {
				c.Accounts[i].Mail.Password = q.Password
				return c.Validate()
			}
		}
		return servercfg.ErrConfig
	case "diagnostics":
		if q.Diagnostics != "on" && q.Diagnostics != "off" {
			return servercfg.ErrConfig
		}
		c.DiagnosticsMode = q.Diagnostics
		return nil
	case "transport":
		if q.Transport == nil {
			return servercfg.ErrConfig
		}
		v := q.Transport
		c.Transport = servercfg.Transport{Layout: v.Layout, DataShards: v.DataShards, AccountSessions: v.AccountSessions, EndpointConnections: v.EndpointConnections, LoginParallelism: v.LoginParallelism}
		return c.Validate()
	case "service_start", "service_stop", "service_restart", "service_install":
		return nil
	}
	return servercfg.ErrConfig
}

func watchdogAvailable() bool {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return false
	}
	if _, e := exec.LookPath("systemd-run"); e != nil {
		return false
	}
	exe, e := os.Executable()
	if e != nil || filepath.Clean(exe) != "/usr/local/bin/itom" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pid, e := exec.CommandContext(ctx, "systemctl", "show", "--property=MainPID", "--value", "itom.service").Output()
	return e == nil && strings.TrimSpace(string(pid)) == strconv.Itoa(os.Getpid())
}

func serviceState(ctx context.Context) string {
	v, e := limitedCommand(ctx, "systemctl", "is-active", "itom.service")
	if e != nil && strings.TrimSpace(v) == "" {
		return "unavailable"
	}
	return strings.TrimSpace(v)
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > b.max {
		return 0, errors.New("ADMIN_OUTPUT_LIMIT")
	}
	_, e := b.Buffer.Write(p)
	return n, e
}
func limitedCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out limitedBuffer
	out.max = 128 << 10
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout = &out
	c.Stderr = io.Discard
	e := c.Run()
	return out.String(), e
}
func tailFile(path string, max int64) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return "", e
	}
	at := maxInt64(0, st.Size()-max)
	if _, e = f.Seek(at, io.SeekStart); e != nil {
		return "", e
	}
	b, e := io.ReadAll(io.LimitReader(f, max))
	if at > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return string(b), e
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Server) startCheck(user servercfg.User, c servercfg.Config, id string) adminapi.Response {
	s.mu.Lock()
	for _, j := range s.jobs {
		if j.Owner == user.ID && !j.Done {
			s.mu.Unlock()
			return adminapi.Response{OK: true, ID: j.ID}
		}
	}
	var accounts []servercfg.Account
	for _, a := range c.Accounts {
		if id == "" || a.ID == id {
			accounts = append(accounts, a)
		}
	}
	if len(accounts) == 0 {
		s.mu.Unlock()
		return adminapi.Response{Error: "ADMIN_ACCOUNT_NOT_FOUND"}
	}
	jid, e := adminapi.NewID()
	if e != nil {
		s.mu.Unlock()
		return adminapi.Response{Error: "ADMIN_CHECK_FAILED"}
	}
	for key, j := range s.jobs {
		if j.Done && s.Now().Sub(j.Created) > 10*time.Minute {
			delete(s.jobs, key)
		}
	}
	j := &job{ID: jid, Owner: user.ID, Created: s.Now()}
	s.jobs[jid] = j
	s.mu.Unlock()
	go func() {
		for _, a := range accounts {
			ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
			var folders []string
			for _, user := range c.Users {
				if user.Enabled {
					folders = append(folders, user.Up, user.Down)
				}
			}
			e := mailbox.CheckExistingFolders(ctx, a.Mail, folders)
			result := "TLS / LOGIN / mailbox selection OK"
			cancel()
			if e != nil {
				result = "Check failed"
				if errors.Is(e, mailbox.ErrMailboxStateChanged) {
					result = "Provider mailbox state changed; retry"
				}
			}
			s.mu.Lock()
			j.Text += a.ID + " · " + a.Mail.Username + ": " + result + "\n"
			s.mu.Unlock()
			if s.ctx.Err() != nil {
				break
			}
		}
		s.mu.Lock()
		j.Done = true
		s.mu.Unlock()
	}()
	return adminapi.Response{OK: true, ID: jid, Text: "Account checks started."}
}

func configRevision(c servercfg.Config) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
