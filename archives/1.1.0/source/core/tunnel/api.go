// Package tunnel retains the native/admin API boundary during the transport
// replacement. The removed runtime cannot be selected or silently reactivated.
package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/socks"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"
)

// Dial carries raw protected network sockets into the replacement TLS executor.
type Dial = transport.NetworkDialer

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

// SelectedLines reports authenticated SELECTs only, never usable VPN capacity.
// The callback is serialized and contains fixed phases and counts, no profile.
type StartupProgress struct {
	Stage                     string
	ConfigHash                string
	FailureReason             string
	RequestKeywordBytes       int
	SelectedLines, TotalLines int
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
}
type Client struct {
	endpoint *transport.EndpointSession
	profile  servercfg.Profile
	mu       sync.Mutex
	events   []string
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	probe    ProbeSnapshot
}

func StartClient(ctx context.Context, p servercfg.Profile, dir string, d Dial) (*Client, error) {
	return StartClientWithOptions(ctx, p, dir, d, ClientOptions{})
}
func StartClientWithOptions(ctx context.Context, p servercfg.Profile, dir string, d Dial, o ClientOptions) (client *Client, err error) {
	stage := "PROFILE"
	started := time.Now()
	// Record fixed, nonsecret startup transitions before opening any provider
	// socket or returning a native Client. Failed construction has no runtime
	// object from which Android could retrieve a trace later.
	startupTrace := &serverTrace{root: dir, enabled: true, startup: true}
	accountTrace := &serverTrace{root: dir, enabled: o.Diagnostics}
	var progressMu sync.Mutex
	progress := StartupProgress{Stage: stage, TotalLines: 2 * len(p.Accounts)}
	report := func(code string, selected bool) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if code != "" {
			progress.Stage = code
		}
		if selected {
			progress.SelectedLines++
		}
		_ = startupTrace.record(struct {
			Event, Revision string
			StartupProgress
			ElapsedMS, SampledAtMS int64
		}{"client_startup", TransportRevision, progress, time.Since(started).Milliseconds(), time.Now().UnixMilli()})
		if o.Progress != nil {
			o.Progress(progress)
		}
	}
	report(stage, false)
	defer func() {
		if err != nil {
			progressMu.Lock()
			progress.FailureReason = startupFailureCode(err)
			progressMu.Unlock()
			report(stage, false)
			err = &startupError{code: "V2_STARTUP_" + stage, cause: err}
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, template, err := sessionPlan(p)
	if err != nil {
		return nil, err
	}
	progress.ConfigHash = hex.EncodeToString(template.Binding.ConfigHash[:])
	if d == nil {
		d = &net.Dialer{Timeout: 20 * time.Second}
	}
	// One client startup budget includes all account logins and negotiation.
	// Runtime lifetime remains attached to ctx, not this temporary deadline.
	startup, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	stage = "WALLET"
	report(stage, false)
	anchor, paths, err := walletPaths(dir, p, plan)
	if err != nil {
		return nil, err
	}
	stage = "IMAP"
	report(stage, false)
	lanes, err := selectProfile(startup, p, plan, nil, d, o.TLSConfig, func() { report("", true) }, func(e selectionEvent) {
		_ = accountTrace.record(struct {
			Event, Revision string
			selectionEvent
			SampledAtMS int64
		}{"client_account_startup", TransportRevision, e, time.Now().UnixMilli()})
	})
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(p.User.Key)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	stage = "RENDEZVOUS"
	deadline, _ := startup.Deadline()
	endpoint, err := transport.StartEndpoint(ctx, transport.EndpointOptions{Client: true, StartupDeadline: deadline, Template: template, Plan: plan, Lanes: lanes, AnchorWallet: anchor, Wallets: paths, Key: key, Service: transport.RosterServiceOptions{ReceiveWindow: 256 << 10}, Stage: func(code string) {
		switch code {
		case "rendezvous":
			stage = "RENDEZVOUS"
		case "rendezvous_found":
			stage = "WALLET"
		case "request", "request_59", "request_31", "request_16":
			stage = "REQUEST"
			progressMu.Lock()
			progress.RequestKeywordBytes = map[string]int{"request": 103, "request_59": 59, "request_31": 31, "request_16": 16}[code]
			progressMu.Unlock()
		case "waiting_invitation":
			stage = "INVITATION"
		case "noise":
			stage = "NOISE"
		case "roster":
			stage = "ROSTER"
		case "runtime":
			stage = "RUNTIME"
		case "permission":
			stage = "PERMISSION"
		case "ready":
			stage = "READY"
		}
		report(stage, false)
	}})
	if err != nil {
		return nil, err
	}
	monitor, cancelMonitor := context.WithCancel(ctx)
	client = &Client{endpoint: endpoint, profile: p, cancel: cancelMonitor}
	client.workers.Add(1)
	go client.runProbe(monitor)
	if o.Diagnostics {
		client.workers.Add(1)
		go client.traceRuntime(monitor, accountTrace)
	}
	return client, nil
}

// Error exposes only a fixed startup phase across gomobile. The Go caller can
// still inspect the cause with errors.Is/As; provider text and private paths
// never become UI messages or diagnostic exports.
type startupError struct {
	code  string
	cause error
}

func (e *startupError) Error() string {
	for _, feature := range []error{errProviderUIDPlus, errProviderKeywords} {
		if errors.Is(e.cause, feature) {
			return feature.Error()
		}
	}
	return e.code
}
func (e *startupError) Unwrap() error { return e.cause }
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	err := c.endpoint.Close()
	c.workers.Wait()
	return err
}
func (c *Client) Stats() Stats { return c.Snapshot().Stats }
func (c *Client) Failure() string {
	select {
	case <-c.endpoint.Done():
		return "V2_TRANSPORT_STOPPED"
	default:
		return ""
	}
}
func (c *Client) Snapshot() ClientSnapshot {
	r := c.endpoint.Service.Runtime.Snapshot()
	s := ClientSnapshot{SampledAtMS: time.Now().UnixMilli()}
	s.TCP = r.Session.Streams
	s.TotalLines = len(r.Carriers)
	for _, a := range c.profile.Accounts {
		line := LineDiagnostic{Account: a.Mail.Username, Stage: string(r.Phase)}
		for _, carrier := range r.Carriers {
			if carrier.Account == a.ID {
				line.TotalLines++
				if carrier.Read == transport.ServiceServing && carrier.Write == transport.ServiceServing {
					line.ActiveLines++
				} else if carrier.ReadFailure != "" || carrier.WriteFailure != "" {
					line.ReadinessReason = "carrier_service_failed"
				}
			}
		}
		line.Ready = line.TotalLines == 2 && line.ActiveLines == 2
		s.ActiveLines += line.ActiveLines
		s.Lines = append(s.Lines, line)
	}
	c.mu.Lock()
	s.Events = append([]string(nil), c.events...)
	s.RTTMillis = c.probe.RTTMillis
	c.mu.Unlock()
	if err := c.endpoint.Err(); err != nil {
		s.Events = append(s.Events, err.Error())
	}
	return s
}
func (c *Client) Diagnostics() string {
	raw, _ := json.Marshal(struct {
		Revision string
		Client   ClientSnapshot
		Runtime  transport.RuntimeSnapshot
		Probe    ProbeSnapshot
	}{TransportRevision, c.Snapshot(), c.endpoint.Service.Runtime.Snapshot(), c.Probe()})
	return string(raw)
}
func (c *Client) RecordNetworkEvent(code string, _ int64) {
	// Only bounded application-defined event codes; never caller/provider strings.
	if len(code) > 64 {
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
	conn, err := c.endpoint.Service.Dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
	if err != nil {
		return nil, netip.AddrPort{}, err
	}
	stream, ok := conn.(socks.Stream)
	if !ok {
		conn.Close()
		return nil, netip.AddrPort{}, transport.ErrProtocol
	}
	return stream, netip.AddrPort{}, nil
}
func Serve(ctx context.Context, c servercfg.Config, dir string, log func(string)) error {
	return ServeManaged(ctx, c, dir, log, nil)
}
func ServeManaged(ctx context.Context, c servercfg.Config, dir string, log func(string), managed func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trace := &serverTrace{root: dir, enabled: c.DiagnosticsMode != "off"}
	if err := trace.record(map[string]any{"Event": "server_start", "Revision": TransportRevision, "Accounts": len(c.Accounts), "Users": len(c.Users), "SampledAtMS": time.Now().UnixMilli()}); err != nil {
		return errors.New("V2_DIAGNOSTICS_UNAVAILABLE")
	}
	if err := c.Validate(); err != nil {
		_ = trace.record(map[string]any{"Event": "configuration_failed", "Revision": TransportRevision, "Failure": "SERVER_CONFIG", "SampledAtMS": time.Now().UnixMilli()})
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, len(c.Users))
	active := 0
	for _, u := range c.Users {
		if !u.Enabled {
			continue
		}
		active++
		p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: c.Server, Provider: c.Provider, User: u, Accounts: c.Accounts, Transport: c.TransportForUsers()}
		go func() {
			done <- serveProfile(ctx, p, dir, log, managed, &net.Dialer{Timeout: 20 * time.Second}, nil, trace)
		}()
	}
	if active == 0 {
		return servercfg.ErrConfig
	}
	var result error
	for range active {
		err := <-done
		if err != nil {
			result = errors.Join(result, err)
			cancel()
		}
	}
	return result
}

const TrafficFile = "traffic.json"

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
	var s TrafficSnapshot
	b, e := os.ReadFile(path)
	if e == nil {
		e = json.Unmarshal(b, &s)
	}
	return s, e
}
