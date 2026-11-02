// Package tunnel is the Android/manager boundary for the new mailbox stream
// runtime. The retired session-v2 implementation is outside the source module;
// there is no selectable legacy data path or wallet/grant startup dependency.
package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/socks"
	"itom.local/core/wire"
	mailstream "itom.local/mailstream"
)

const TransportRevision = "itom-1-1-0-mailstream-rc2"
const pathHost = "path.mailstream.itom.invalid"

type Dial = mailstream.NetworkDialer

func NetworkDial(d mailbox.NetworkDialer) Dial { return d }

type Stats struct {
	ActiveLines, TotalLines, TCP int
	RTTMillis                    int64
}
type ClientOptions struct {
	Diagnostics bool
	TLSConfig   *tls.Config
	Progress    func(StartupProgress)
}
type StartupProgress struct {
	Stage, ConfigHash, FailureReason string
	RequestKeywordBytes              int
	SelectedLines, TotalLines        int
}
type ClientSnapshot struct {
	Stats
	ServerMessage         string
	ServerMessageRevision uint64
	SampledAtMS           int64
	Lines                 []LineDiagnostic
	Events                []string
}
type LineDiagnostic struct {
	Account, Stage, ReadinessReason string
	Ready                           bool
	ActiveLines, TotalLines         int
	RTTMillis, IMAPLatencyMillis    int64
	RTTFresh                        bool
	UploadBytes, DownloadBytes      uint64
	TCPStreams                      int
}
type ProbeSnapshot struct {
	Samples, Failures      uint64
	RTTMillis, SampledAtMS int64
	Failure                string
}
type Client struct {
	session   *mailstream.Session
	link      *mailstream.IMAPLink
	profile   servercfg.Profile
	cancel    context.CancelFunc
	workers   sync.WaitGroup
	mu        sync.Mutex
	probe     ProbeSnapshot
	events    []string
	trace     *traceWriter
	closeOnce sync.Once
}

func profileIdentity(p servercfg.Profile) string {
	b := append(append([]byte("ITOM-mailstream-profile\x00"), p.Server[:]...), []byte("\x00"+p.User.ID)...)
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:16])
}

// Metadata-only fingerprint for matching startup evidence. Passwords, TLS
// internals and per-session secrets never enter diagnostics or this digest.
func profileConfigHash(p servercfg.Profile) string {
	type account struct {
		ID, Host, Username string
		Port               int
	}
	value := struct {
		Identity string
		Accounts []account
	}{Identity: profileIdentity(p)}
	for _, a := range p.Accounts {
		value.Accounts = append(value.Accounts, account{a.ID, a.Mail.Host, a.Mail.Username, a.Mail.Port})
	}
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func profileAccounts(p servercfg.Profile, d Dial, roots *tls.Config) ([]mailstream.MailAccount, []byte, error) {
	if p.Schema != 1 || p.Kind != "itom-imap-probe" || !p.User.Enabled || len(p.Accounts) == 0 {
		return nil, nil, servercfg.ErrConfig
	}
	if err := (servercfg.Config{Schema: 1, Server: p.Server, Provider: p.Provider, Users: []servercfg.User{p.User}, Accounts: p.Accounts, Transport: p.Transport}).Validate(); err != nil {
		return nil, nil, err
	}
	limits := p.Transport.Effective()
	// Every configured endpoint needs control receive + writer. A live user
	// adds one DATA reader on both ends; admission enforces that last pair.
	if limits.AccountSessions < 3*limits.Participants+4 || limits.EndpointConnections < (limits.Participants+3)*len(p.Accounts) {
		return nil, nil, errors.New("MAILSTREAM_SOCKET_BUDGET")
	}
	if roots != nil && roots.InsecureSkipVerify {
		return nil, nil, mailstream.ErrAuth
	}
	key, err := hex.DecodeString(p.User.Key)
	if err != nil || len(key) != 32 {
		return nil, nil, servercfg.ErrConfig
	}
	var accounts []mailstream.MailAccount
	for _, a := range p.Accounts {
		if a.Mail.TLS != "tls" {
			clear(key)
			return nil, nil, errors.New("MAILSTREAM_TLS_REQUIRED")
		}
		account := mailstream.MailAccount{ID: a.ID, Host: a.Mail.Host, DialIP: a.Mail.DialIP, Port: a.Mail.Port, Username: a.Mail.Username, Password: a.Mail.Password, Dialer: d}
		if roots != nil {
			account.Roots = roots.RootCAs
		}
		accounts = append(accounts, account)
	}
	return accounts, key, nil
}

func StartClient(ctx context.Context, p servercfg.Profile, dir string, d Dial) (*Client, error) {
	return StartClientWithOptions(ctx, p, dir, d, ClientOptions{})
}
func StartClientWithOptions(ctx context.Context, p servercfg.Profile, dir string, d Dial, o ClientOptions) (client *Client, err error) {
	trace, err := newTrace(dir)
	if err != nil {
		return nil, err
	}
	stage := "PROFILE"
	identity := profileIdentity(p)
	configHash := profileConfigHash(p)
	var progressMu sync.Mutex
	report := func(failure string, link *mailstream.IMAPLink) {
		progressMu.Lock()
		defer progressMu.Unlock()
		n := 0
		if link != nil {
			snap := link.Snapshot()
			n = snap.Ready + snap.Writers + snap.Data
		}
		progress := StartupProgress{Stage: stage, ConfigHash: configHash, FailureReason: failure, SelectedLines: n, TotalLines: 3 * len(p.Accounts)}
		record := map[string]any{"Event": "startup", "Revision": TransportRevision, "Progress": progress, "SampledAtMS": time.Now().UnixMilli()}
		if link != nil {
			record["Mail"] = link.Snapshot()
		}
		_ = trace.record(record)
		if o.Progress != nil {
			o.Progress(progress)
		}
	}
	report("", nil)
	defer func() {
		if err != nil {
			report(failureCode(err), nil)
			err = &startupError{stage, err}
		}
	}()
	accounts, key, err := profileAccounts(p, d, o.TLSConfig)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	life, cancel := context.WithCancel(ctx)
	stage = "STORAGE"
	report("", nil)
	link, err := mailstream.NewStoredIMAPLink(life, identity, accounts, true, dir)
	if err != nil {
		cancel()
		return nil, err
	}
	owned := false
	defer func() {
		if !owned {
			cancel()
			link.Close()
		}
	}()
	startup, stop := context.WithTimeout(life, 45*time.Second)
	defer stop()
	stage = "NOISE"
	report("", link)
	status, stopStatus := context.WithCancel(startup)
	statusDone := make(chan struct{})
	go func() {
		defer close(statusDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-status.Done():
				return
			case <-tick.C:
				report("", link)
			}
		}
	}()
	defer func() { stopStatus(); <-statusDone }()
	session, err := mailstream.Connect(startup, link, identity, key)
	if err != nil {
		if link.Snapshot().Ready == 0 {
			progressMu.Lock()
			stage = "IMAP"
			progressMu.Unlock()
		}
		return nil, err
	}
	client = &Client{session: session, link: link, profile: p, cancel: cancel, trace: trace}
	progressMu.Lock()
	stage = "PATH"
	progressMu.Unlock()
	report("", link)
	if err = verifyDataPath(startup, session); err != nil {
		session.Close()
		return nil, err
	}
	if _, err = client.samplePing(startup); err != nil {
		session.Close()
		return nil, err
	}
	progressMu.Lock()
	stage = "READY"
	progressMu.Unlock()
	report("", link)
	_ = trace.record(map[string]any{"Event": "client_runtime", "Revision": TransportRevision, "Client": client.Snapshot(), "Session": session.Snapshot(), "Mail": link.Snapshot(), "SampledAtMS": time.Now().UnixMilli()})
	owned = true
	client.workers.Add(1)
	go func() {
		defer client.workers.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-life.Done():
				session.Close()
				return
			case <-session.Done():
				cancel()
				return
			case <-ticker.C:
				probe, done := context.WithTimeout(life, 5*time.Second)
				_, _ = client.samplePing(probe)
				done()
				if o.Diagnostics {
					_ = trace.record(map[string]any{"Event": "client_runtime", "Revision": TransportRevision, "Client": client.Snapshot(), "Session": session.Snapshot(), "Mail": link.Snapshot(), "SampledAtMS": time.Now().UnixMilli()})
				}
			}
		}
	}()
	return client, nil
}

func verifyDataPath(ctx context.Context, session *mailstream.Session) error {
	conn, err := session.OpenTCP(ctx, net.JoinHostPort(pathHost, "1"))
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	probe := make([]byte, 16<<10)
	if _, err = rand.Read(probe); err != nil {
		return err
	}
	defer clear(probe)
	if _, err = io.Copy(conn, bytes.NewReader(probe)); err != nil {
		return err
	}
	echo := make([]byte, len(probe))
	defer clear(echo)
	if _, err = io.ReadFull(conn, echo); err != nil {
		return err
	}
	if !bytes.Equal(probe, echo) {
		return mailstream.ErrProtocol
	}
	return nil
}

type startupError struct {
	stage string
	cause error
}

func (e *startupError) Error() string {
	if errors.Is(e.cause, mailstream.ErrBusy) {
		return "MAILSTREAM_SERVER_BUSY"
	}
	return "MAILSTREAM_STARTUP_" + e.stage
}
func (e *startupError) Unwrap() error { return e.cause }
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() { c.cancel(); c.session.Close(); c.link.Close(); c.workers.Wait() })
	return nil
}
func (c *Client) samplePing(ctx context.Context) (time.Duration, error) {
	d, err := c.session.Ping(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probe.SampledAtMS = time.Now().UnixMilli()
	if err != nil {
		c.probe.Failures++
		c.probe.Failure = failureCode(err)
	} else {
		c.probe.Samples++
		c.probe.RTTMillis = d.Milliseconds()
		c.probe.Failure = ""
	}
	return d, err
}
func (c *Client) Ping(ctx context.Context) (time.Duration, error) { return c.samplePing(ctx) }
func (c *Client) Probe() ProbeSnapshot                            { c.mu.Lock(); defer c.mu.Unlock(); return c.probe }
func (c *Client) Stats() Stats                                    { return c.Snapshot().Stats }
func (c *Client) Failure() string {
	if c.session.Err() != nil {
		return "MAILSTREAM_STOPPED"
	}
	return ""
}
func (c *Client) Snapshot() ClientSnapshot {
	mail := c.link.Snapshot()
	session := c.session.Snapshot()
	streams := c.session.ActiveStreams()
	s := ClientSnapshot{Stats: Stats{TotalLines: 3 * len(c.profile.Accounts), TCP: session.Streams}, SampledAtMS: time.Now().UnixMilli()}
	for _, line := range mail.Lines {
		account := line.Account
		for _, a := range c.profile.Accounts {
			if a.ID == account {
				account = a.Mail.Username
				break
			}
		}
		active := 0
		if line.Ready {
			active++
		}
		if line.Writer {
			active++
		}
		if line.Data {
			active++
		}
		s.ActiveLines += active
		s.Lines = append(s.Lines, LineDiagnostic{Account: account, Stage: line.Stage, ReadinessReason: line.Failure, Ready: active == 3, ActiveLines: active, TotalLines: 3,
			RTTMillis: line.RTTMillis, RTTFresh: line.RTTFresh, IMAPLatencyMillis: line.IMAPLatencyMillis,
			UploadBytes: line.UploadBytes, DownloadBytes: line.DownloadBytes, TCPStreams: c.link.AccountStreams(line.Account, streams)})
	}
	c.mu.Lock()
	s.RTTMillis = c.probe.RTTMillis
	s.Events = append([]string(nil), c.events...)
	c.mu.Unlock()
	return s
}
func (c *Client) Diagnostics() string {
	raw, _ := json.Marshal(map[string]any{"Revision": TransportRevision, "Client": c.Snapshot(), "Session": c.session.Snapshot(), "Mail": c.link.Snapshot(), "Events": c.link.Events(), "Probe": c.Probe()})
	return string(raw)
}
func (c *Client) RecordNetworkEvent(code string, _ int64) {
	if len(code) == 0 || len(code) > 64 {
		return
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 64 {
		copy(c.events, c.events[1:])
		c.events = c.events[:63]
	}
	c.events = append(c.events, code)
}
func (c *Client) RecordApplicationCopyEvent(_ uint64, code string, _ int64, _ error, _ string) {
	c.RecordNetworkEvent(code, 0)
}
func (c *Client) Open(ctx context.Context, target wire.Connect) (socks.Stream, netip.AddrPort, error) {
	conn, err := c.session.OpenTCP(ctx, net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
	if err != nil {
		return nil, netip.AddrPort{}, err
	}
	stream, ok := conn.(socks.Stream)
	if !ok {
		conn.Close()
		return nil, netip.AddrPort{}, mailstream.ErrProtocol
	}
	return stream, netip.AddrPort{}, nil
}

func diagnosticPath(dir string) string { return filepath.Join(dir, "transport-trace.jsonl") }
