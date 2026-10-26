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
}

func StartClient(ctx context.Context, p servercfg.Profile, dir string, d Dial) (*Client, error) {
	return StartClientWithOptions(ctx, p, dir, d, ClientOptions{})
}
func StartClientWithOptions(ctx context.Context, p servercfg.Profile, dir string, d Dial, o ClientOptions) (*Client, error) {
	plan, template, err := sessionPlan(p)
	if err != nil {
		return nil, err
	}
	if d == nil {
		d = &net.Dialer{Timeout: 20 * time.Second}
	}
	anchor, paths, err := walletPaths(dir, p, plan)
	if err != nil {
		return nil, err
	}
	lanes, err := selectProfile(ctx, p, plan, nil, d, o.TLSConfig)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(p.User.Key)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	endpoint, err := transport.StartEndpoint(ctx, transport.EndpointOptions{Client: true, Template: template, Plan: plan, Lanes: lanes, AnchorWallet: anchor, Wallets: paths, Key: key, Service: transport.RosterServiceOptions{ReceiveWindow: 256 << 10}})
	if err != nil {
		return nil, err
	}
	return &Client{endpoint: endpoint, profile: p}, nil
}
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	return c.endpoint.Close()
}
func (c *Client) Stats() Stats { return c.Snapshot().Stats }
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
	}{TransportRevision, c.Snapshot(), c.endpoint.Service.Runtime.Snapshot()})
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
	if err := c.Validate(); err != nil {
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
		go func() { done <- serveProfile(ctx, p, dir, log, managed, &net.Dialer{Timeout: 20 * time.Second}, nil) }()
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
