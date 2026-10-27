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

	"itom.local/core/journal"
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

func serveProfile(ctx context.Context, p servercfg.Profile, root string, log func(string), managed func(context.Context, servercfg.User, wire.Connect) (*net.TCPConn, bool, error), network transport.NetworkDialer, tlsConfig *tls.Config, trace *serverTrace) (err error) {
	phase := "profile"
	binding := ""
	observe := func(stage string, event selectionEvent, failure error) {
		reason := event.Failure
		if failure != nil {
			reason = startupFailureCode(failure)
		}
		raw, _ := json.Marshal(map[string]any{
			"Event": "startup", "Revision": TransportRevision, "Stage": stage,
			"User": p.User.ID, "ConfigHash": binding, "Account": event.Account,
			"MailboxRole": event.Role, "UIDPlus": event.UIDPlus,
			"PermanentKeywords": event.Keywords, "Failure": reason,
			"SampledAtMS": time.Now().UnixMilli(),
		})
		if e := trace.record(json.RawMessage(raw)); e != nil && log != nil {
			log("event=v2_diagnostics state=write_failed")
		}
		if len(p.User.ID) == 8 && p.User.ID[0] >= '1' && p.User.ID[0] <= '9' {
			valid := true
			for _, c := range p.User.ID {
				if c < '0' || c > '9' {
					valid = false
				}
			}
			if valid {
				_ = trace.snapshot(p.User.ID, raw)
			}
		}
		if log != nil {
			log("event=v2_startup state=" + stage + " reason=" + reason + " account=" + event.Account)
		}
	}
	observe(phase, selectionEvent{}, nil)
	defer func() {
		if err != nil {
			observe(phase+"_failed", selectionEvent{}, err)
		}
	}()
	plan, template, err := sessionPlan(p)
	if err != nil {
		return err
	}
	binding = hex.EncodeToString(template.Binding.ConfigHash[:])
	phase = "journals"
	observe(phase, selectionEvent{}, nil)
	journals, err := openProfileJournals(root, p)
	if err != nil {
		return err
	}
	defer closeProfileJournals(journals)
	for _, j := range journals {
		for name, store := range map[string]*journal.Store{"control": j.Control, "data": j.Data} {
			limit, le := store.Limit(j.Account, p.User.ID)
			held, he := store.Held(j.Account, p.User.ID)
			if le == nil && he == nil {
				_ = trace.record(map[string]any{
					"Event": "journal_capacity", "Revision": TransportRevision,
					"User": p.User.ID, "Account": j.Account, "Ledger": name,
					"Limit": limit, "Held": held, "SampledAtMS": time.Now().UnixMilli(),
				})
			}
		}
	}
	key, err := hex.DecodeString(p.User.Key)
	if err != nil {
		return err
	}
	defer clear(key)
	for ctx.Err() == nil {
		phase = "inventory"
		observe(phase, selectionEvent{}, nil)
		lanes, err := selectProfile(ctx, p, plan, journals, network, tlsConfig, nil, func(e selectionEvent) { observe(e.Stage, e, nil) })
		if err != nil {
			return err
		}
		options := transport.RosterServiceOptions{ReceiveWindow: 256 << 10, ExitWorkers: 16, Dialer: network, Policy: transport.ExitPolicyFunc(publicExit)}
		options.ManagedDial = func(ctx context.Context, user string, target transport.Target) (net.Conn, bool, error) {
			if user != p.User.ID {
				return nil, true, transport.OpenDenied
			}
			if conn, handled, err := echoExit(target); handled {
				return conn, handled, err
			}
			if managed == nil {
				return nil, false, nil
			}
			conn, handled, err := managed(ctx, p.User, wire.Connect{Host: target.Host, Port: target.Port})
			// A typed nil *TCPConn is not a live managed destination.
			if conn == nil {
				return nil, handled, err
			}
			return conn, handled, err
		}

		stage := func(code string) {
			phase = code
			observe(code, selectionEvent{}, nil)
			var imap []transport.ExecutorSnapshot
			for _, lane := range lanes {
				imap = append(imap, lane.Executor.Snapshot())
			}
			raw, _ := json.Marshal(struct {
				Revision, Stage, ConfigHash string
				SampledAtMS                 int64
				Carriers                    []transport.ExecutorSnapshot
			}{TransportRevision, code, binding, time.Now().UnixMilli(), imap})
			_ = trace.record(json.RawMessage(raw))
			_ = trace.snapshot(p.User.ID, raw)
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
				_ = trace.record(json.RawMessage(raw))
				if err := trace.snapshot(p.User.ID, raw); err != nil {
					if log != nil {
						log("event=v2_diagnostics state=write_failed")
					}
				}
			}
		}
		ticker.Stop()
		err = endpoint.Close()
		if err != nil && !errors.Is(err, transport.ErrEndpointRestart) && ctx.Err() == nil {
			_ = trace.record(struct {
				Event, Revision string
				SampledAtMS     int64
				Runtime         transport.RuntimeSnapshot
			}{"runtime_failed", TransportRevision, time.Now().UnixMilli(), endpoint.Service.Runtime.Snapshot()})
		}
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
