package tunnel

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/transport"
	"itom.local/core/wire"
)

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func publicExit(ctx context.Context, _ string, target transport.Target) (netip.AddrPort, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", target.Host)
	if err != nil || len(ips) == 0 {
		return netip.AddrPort{}, transport.OpenDenied
	}
	for _, ip := range ips {
		if !publicAddress(ip) {
			return netip.AddrPort{}, transport.OpenDenied
		}
	}
	return netip.AddrPortFrom(ips[0].Unmap(), target.Port), nil
}

func serveProfile(ctx context.Context, p servercfg.Profile, root string, log func(string), managed func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error), network transport.NetworkDialer, tlsConfig *tls.Config) error {
	plan, template, err := sessionPlan(p)
	if err != nil {
		return err
	}
	journals, err := openProfileJournals(root, p)
	if err != nil {
		return err
	}
	defer closeProfileJournals(journals)
	key, err := hex.DecodeString(p.User.Key)
	if err != nil {
		return err
	}
	defer clear(key)
	for ctx.Err() == nil {
		if log != nil {
			log("event=v2_startup state=inventory user=" + p.User.ID)
		}
		lanes, err := selectProfile(ctx, p, plan, journals, network, tlsConfig)
		if err != nil {
			return err
		}
		options := transport.RosterServiceOptions{ReceiveWindow: 256 << 10, ExitWorkers: 16, Dialer: network, Policy: transport.ExitPolicyFunc(publicExit)}
		if managed != nil {
			options.ManagedDial = func(ctx context.Context, user string, target transport.Target) (net.Conn, bool, error) {
				if user != p.User.ID {
					return nil, true, transport.OpenDenied
				}
				conn, handled, err := managed(ctx, p.User, wire.Connect{Host: target.Host, Port: target.Port})
				// Do not turn a typed nil *TCPConn into a nonnil net.Conn interface.
				if conn == nil {
					return nil, handled, err
				}
				return conn, handled, err
			}
		}
		stage := func(code string) {
			if log != nil {
				log("event=v2_startup state=" + code + " user=" + p.User.ID)
			}
			var imap []transport.ExecutorSnapshot
			for _, lane := range lanes {
				imap = append(imap, lane.Executor.Snapshot())
			}
			raw, _ := json.Marshal(struct {
				Revision, Stage string
				SampledAtMS     int64
				Carriers        []transport.ExecutorSnapshot
			}{TransportRevision, code, time.Now().UnixMilli(), imap})
			_ = writePrivateSnapshot(filepath.Join(root, "v2-"+p.User.ID+".json"), raw)
		}
		endpoint, err := transport.StartEndpoint(ctx, transport.EndpointOptions{Template: template, Plan: plan, Lanes: lanes, Journals: journals, Key: key, Service: options, Stage: stage})
		if err != nil {
			stage("failed")
			for _, lane := range lanes {
				lane.Executor.Close()
			}
			return err
		}
		if log != nil {
			log("event=v2_session state=serving user=" + p.User.ID)
		}
		ticker := time.NewTicker(2 * time.Second)
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				waiting = false
			case <-endpoint.Done():
				waiting = false
			case <-ticker.C:
				// Bounded causal diagnostics contain no profiles, keys or payloads.
				raw, _ := json.Marshal(struct {
					Revision    string
					SampledAtMS int64
					Runtime     transport.RuntimeSnapshot
				}{TransportRevision, time.Now().UnixMilli(), endpoint.Service.Runtime.Snapshot()})
				if err := writePrivateSnapshot(filepath.Join(root, "v2-"+p.User.ID+".json"), raw); err != nil {
					if log != nil {
						log("event=v2_diagnostics state=write_failed")
					}
				}
			}
		}
		ticker.Stop()
		err = endpoint.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, transport.ErrEndpointRestart) {
			return errors.Join(transport.ErrRuntimeStopped, err)
		}
		if log != nil {
			log("event=v2_session state=user_restart user=" + p.User.ID)
		}
	}
	return ctx.Err()
}

func writePrivateSnapshot(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".v2-diagnostic-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
