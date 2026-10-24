// Package tunnel retains the native/admin API boundary during the transport
// replacement. The removed runtime cannot be selected or silently reactivated.
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/socks"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"os"
)

const TransportRevision = "1.1.0-session-v2-unqualified"

var ErrNotReady = errors.New("V2_NOT_READY: authenticated bootstrap, durable carrier integration and full qualification are incomplete")

// Dial is retained for source compatibility only, not used by the v2 executor.
type Dial func(context.Context, mailbox.Config, []string) (*mailbox.Client, error)

func NetworkDial(d mailbox.NetworkDialer) Dial {
	return func(ctx context.Context, c mailbox.Config, f []string) (*mailbox.Client, error) {
		return mailbox.DialWithNetwork(ctx, c, f, d)
	}
}

type Stats struct {
	ActiveLines, TotalLines, TCP int
	RTTMillis                    int64
}
type ClientOptions struct{ Diagnostics bool }
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
type Client struct{}

func StartClient(ctx context.Context, p servercfg.Profile, dir string, d Dial) (*Client, error) {
	return StartClientWithOptions(ctx, p, dir, d, ClientOptions{})
}
func StartClientWithOptions(context.Context, servercfg.Profile, string, Dial, ClientOptions) (*Client, error) {
	return nil, ErrNotReady
}
func (*Client) Close() error { return nil }
func (*Client) Stats() Stats { return Stats{} }
func (*Client) Snapshot() ClientSnapshot {
	return ClientSnapshot{Events: []string{ErrNotReady.Error()}}
}
func (*Client) Diagnostics() string                                             { return ErrNotReady.Error() }
func (*Client) RecordNetworkEvent(string, int64)                                {}
func (*Client) RecordApplicationCopyEvent(uint64, string, int64, error, string) {}
func (*Client) Open(context.Context, wire.Connect) (socks.Stream, netip.AddrPort, error) {
	return nil, netip.AddrPort{}, ErrNotReady
}
func Serve(context.Context, servercfg.Config, string, func(string)) error { return ErrNotReady }
func ServeManaged(context.Context, servercfg.Config, string, func(string), func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error)) error {
	return ErrNotReady
}
func MigrateDataProfile(string, servercfg.Account, servercfg.User, string) error { return ErrNotReady }

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
