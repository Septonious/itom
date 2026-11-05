// Compiled unchanged against either frozen RC2 or the candidate core module.
// The adapter changes only permission to reach the two local fixture origins.
// It never replaces the production transport, grants, crypto, or mailbox dialer.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

type providerConfig struct {
	ClientPort         int `json:"client_port"`
	ServerPort         int `json:"server_port"`
	Root, Key, Metrics string
	Accounts           int
}
type clock struct {
	mu              sync.Mutex
	start           time.Time
	buckets         []uint64
	total           uint64
	last, sendUntil time.Time
	maxGap          time.Duration
}

func (c *clock) add(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.noteGap(now)
	c.last = now
	c.total += uint64(n)
	i := int(now.Sub(c.start) / (500 * time.Millisecond))
	for len(c.buckets) <= i {
		c.buckets = append(c.buckets, 0)
	}
	c.buckets[i] += uint64(n)
}
func (c *clock) result() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noteGap(time.Now())
	if !c.sendUntil.IsZero() {
		end := time.Now()
		if end.After(c.sendUntil) {
			end = c.sendUntil
		}
		required := int((end.Sub(c.start) + (500 * time.Millisecond) - 1) / (500 * time.Millisecond))
		for len(c.buckets) < required {
			c.buckets = append(c.buckets, 0)
		}
	}
	rates := make([]float64, len(c.buckets))
	for i, b := range c.buckets {
		rates[i] = float64(b) * 8 / 0.5 / 1e6
	}
	return map[string]any{"verified_bytes": c.total, "half_second_mbps": rates, "max_receiver_gap_after_ramp_ms": float64(c.maxGap.Microseconds()) / 1000}
}

// Caller holds mu. Count any 500 ms gap during the active sending interval,
// including gaps that straddle two nonempty half-second buckets.
func (c *clock) noteGap(now time.Time) {
	if c.sendUntil.IsZero() {
		return
	}
	begin := c.last
	if begin.Before(c.start.Add(2 * time.Second)) {
		begin = c.start.Add(2 * time.Second)
	}
	end := now
	if end.After(c.sendUntil) {
		end = c.sendUntil
	}
	if gap := end.Sub(begin); gap > c.maxGap {
		c.maxGap = gap
	}
}

func (c *clock) stop(duration time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.start.Add(duration)
}

type result struct {
	Version                   string `json:"version"`
	Success                   bool   `json:"success"`
	Stage                     string `json:"stage"`
	Error                     string `json:"error,omitempty"`
	StartupMS                 int64  `json:"startup_ms"`
	IdleMS, LoadedMS, AfterMS []float64
	Phases                    []map[string]any
	Provider                  any
	Diagnostics               json.RawMessage `json:"diagnostics,omitempty"`
	Targets                   map[string]bool `json:"targets"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (err error) {
	config := flag.String("provider", "", "provider descriptor")
	out := flag.String("out", "", "result JSON")
	state := flag.String("state", "", "private state directory")
	label := flag.String("label", "", "immutable source label")
	duration := flag.Duration("duration", 12*time.Second, "application send duration per phase")
	cycles := flag.Int("cycles", 1, "complete speedtests in the same VPN session")
	streams := flag.Int("streams", 1, "simultaneous streams per direction")
	duplex := flag.Bool("duplex", false, "simultaneous upload and download")
	cancelDownload := flag.Duration("cancel-download-after", 0, "simulate a timed speedtest abandoning its download, then verify a new HTTPS connection")
	idleObserve := flag.Duration("idle-observe", 0, "minimum observation time before load")
	idleProbes := flag.Int("idle-probes", 20, "small HTTPS messages before load")
	postProbes := flag.Int("post-probes", 12, "small HTTPS messages after load")
	cpu := flag.String("cpu", "", "CPU profile")
	flag.Parse()
	if *cycles < 1 || *cycles > 5 {
		return errors.New("cycles must be 1..5")
	}
	if *cpu != "" {
		f, e := os.Create(*cpu)
		if e != nil {
			return e
		}
		if e = pprof.StartCPUProfile(f); e != nil {
			return e
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}
	r := result{Version: *label, Stage: "configuration"}
	// Preserve bounded local fixture traces on functional failure as well.
	defer func() {
		for _, side := range []string{"server", "client"} {
			for _, suffix := range []string{"", ".1", ".2"} {
				body, e := os.ReadFile(filepath.Join(*state, side, "transport-trace.jsonl"+suffix))
				if e == nil && len(body) <= 2<<20 {
					_ = os.WriteFile(filepath.Join(filepath.Dir(*out), *label+"-"+side+"-trace.jsonl"+suffix), body, 0600)
				}
			}
		}
	}()
	defer func() {
		if err != nil {
			r.Error = err.Error()
		}
		body, _ := json.MarshalIndent(r, "", "  ")
		_ = os.WriteFile(*out, body, 0600)
	}()
	raw, err := os.ReadFile(*config)
	if err != nil {
		return err
	}
	var p providerConfig
	if err = json.Unmarshal(raw, &p); err != nil {
		return err
	}
	defer func() {
		response, e := (&http.Client{Timeout: 2 * time.Second}).Get(p.Metrics)
		if e == nil {
			defer response.Body.Close()
			_ = json.NewDecoder(response.Body).Decode(&r.Provider)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*(*duration)*time.Duration(*cycles)+90*time.Second)
	defer cancel()
	cert, err := tls.LoadX509KeyPair(p.Root, p.Key)
	if err != nil {
		return err
	}
	rootPEM, err := os.ReadFile(p.Root)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		return errors.New("fixture root invalid")
	}
	up, down := &clock{}, &clock{}
	origin := http.Server{ReadHeaderTimeout: 10 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fresh HTTPS works") })
	mux.HandleFunc("/upload", func(w http.ResponseWriter, q *http.Request) {
		defer q.Body.Close()
		buffer := make([]byte, 64<<10)
		offset := uint64(0)
		for {
			n, e := q.Body.Read(buffer)
			if n > 0 {
				if !valid(buffer[:n], offset) {
					http.Error(w, "integrity", 500)
					return
				}
				offset += uint64(n)
				up.add(n)
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return
			}
		}
		fmt.Fprintf(w, "%d", offset)
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, q *http.Request) {
		buffer := make([]byte, 64<<10)
		offset := uint64(0)
		stop := down.stop(*duration)
		for time.Now().Before(stop) {
			fill(buffer, offset)
			n, e := w.Write(buffer)
			offset += uint64(n)
			if e != nil {
				return
			}
		}
	})
	origin.Handler = mux
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	go origin.Serve(listener)
	defer origin.Close()
	echo, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer echo.Close()
	go func() {
		for {
			conn, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	cfg, err := servercfg.New("performance-fixture")
	if err != nil {
		return err
	}
	user, err := cfg.AddUser("fixture")
	if err != nil {
		return err
	}
	cfg.DiagnosticsMode = "on"
	cfg.Transport = servercfg.Transport{AccountSessions: 10, EndpointConnections: 128, LoginParallelism: 4, Participants: 1}
	for i := 0; i < p.Accounts; i++ {
		name := fmt.Sprintf("a%02d", i+1)
		cfg.Accounts = append(cfg.Accounts, servercfg.Account{ID: name, Mail: mailbox.Config{Host: "127.0.0.1", Port: p.ServerPort, TLS: "tls", Username: name, Password: "fixture-only"}})
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveFixture(ctx, cfg, filepath.Join(*state, "server"), listener.Addr().String(), echo.Addr().String())
	}()
	defer func() {
		cancel()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
		}
	}()
	profile := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: cfg.Server, Provider: cfg.Provider, User: user, Accounts: append([]servercfg.Account(nil), cfg.Accounts...), Transport: cfg.Transport}
	for i := range profile.Accounts {
		profile.Accounts[i].Mail.Port = p.ClientPort
	}
	r.Stage = "startup"
	started := time.Now()
	client, err := tunnel.StartClientWithOptions(ctx, profile, filepath.Join(*state, "client"), nil, tunnel.ClientOptions{Diagnostics: true})
	if err != nil {
		return err
	}
	defer client.Close()
	defer func() { r.Diagnostics = json.RawMessage(client.Diagnostics()) }()
	ready := time.NewTimer(45 * time.Second)
	defer ready.Stop()
	for client.Stats().ActiveLines < client.Stats().TotalLines {
		select {
		case e := <-serverDone:
			return fmt.Errorf("server startup: %v", e)
		case <-ready.C:
			return fmt.Errorf("readiness: %+v", client.Stats())
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	r.StartupMS = time.Since(started).Milliseconds()
	open := func(call context.Context, port uint16) (net.Conn, error) {
		stream, _, e := client.Open(call, wire.Connect{Host: "127.0.0.1", Port: port})
		if e != nil {
			return nil, e
		}
		conn, ok := stream.(net.Conn)
		if !ok {
			stream.Close()
			return nil, errors.New("not net.Conn")
		}
		return conn, nil
	}
	chatRaw, err := open(ctx, uint16(echo.Addr().(*net.TCPAddr).Port))
	if err != nil {
		return err
	}
	defer chatRaw.Close()
	chat := tls.Client(chatRaw, &tls.Config{ServerName: "fixture.example", RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err = chat.HandshakeContext(ctx); err != nil {
		return err
	}
	defer chat.Close()
	ping := func() (float64, error) {
		_ = chat.SetDeadline(time.Now().Add(3 * time.Second))
		start := time.Now()
		body := []byte("small application message")
		if _, e := chat.Write(body); e != nil {
			return 0, e
		}
		got := make([]byte, len(body))
		if _, e := io.ReadFull(chat, got); e != nil {
			return 0, e
		}
		if string(body) != string(got) {
			return 0, errors.New("chat corruption")
		}
		return float64(time.Since(start).Microseconds()) / 1000, nil
	}
	r.Stage = "idle"
	mark := func() error {
		response, e := (&http.Client{Timeout: 2 * time.Second}).Get(strings.TrimSuffix(p.Metrics, "/metrics") + "/phase")
		if e != nil {
			return e
		}
		response.Body.Close()
		return nil
	}
	if err = mark(); err != nil {
		return err
	}
	idleStart := time.Now()
	for i := 0; i < *idleProbes || time.Since(idleStart) < *idleObserve; i++ {
		ms, e := ping()
		if e != nil {
			return e
		}
		r.IdleMS = append(r.IdleMS, ms)
		time.Sleep(40 * time.Millisecond)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: false, MaxConnsPerHost: 2*(*streams) + 4, MaxIdleConnsPerHost: 2*(*streams) + 4, DialContext: func(call context.Context, _, _ string) (net.Conn, error) {
		return open(call, uint16(listener.Addr().(*net.TCPAddr).Port))
	}}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: *duration + 30*time.Second}
	phase := func(upload, download bool) error {
		name := "upload"
		if !upload {
			name = "download"
		}
		if upload && download {
			name = "duplex"
		}
		r.Stage = name
		if e := mark(); e != nil {
			return e
		}
		begin := time.Now()
		up.mu.Lock()
		up.start = begin
		up.buckets = nil
		up.total = 0
		up.last, up.sendUntil, up.maxGap = time.Time{}, time.Time{}, 0
		if upload {
			up.sendUntil = begin.Add(*duration)
		}
		up.mu.Unlock()
		down.mu.Lock()
		down.start = begin
		down.buckets = nil
		down.total = 0
		down.last, down.sendUntil, down.maxGap = time.Time{}, time.Time{}, 0
		if download {
			down.sendUntil = begin.Add(*duration)
		}
		down.mu.Unlock()
		errs := make(chan error, 2*(*streams))
		var wg sync.WaitGroup
		for i := 0; i < *streams; i++ {
			if upload {
				wg.Add(1)
				go func() {
					defer wg.Done()
					reader, writer := io.Pipe()
					defer reader.Close()
					sent := make(chan uint64, 1)
					go func() {
						buffer := make([]byte, 64<<10)
						offset := uint64(0)
						defer func() { sent <- offset }()
						for time.Since(begin) < *duration {
							fill(buffer, offset)
							n, e := writer.Write(buffer)
							offset += uint64(n)
							if e != nil {
								return
							}
						}
						writer.Close()
					}()
					response, e := httpClient.Post("https://fixture.example/upload", "application/octet-stream", reader)
					if e != nil {
						reader.CloseWithError(e)
						errs <- e
						return
					}
					body, e := io.ReadAll(response.Body)
					response.Body.Close()
					if e != nil {
						errs <- e
						return
					}
					if response.StatusCode != 200 {
						errs <- fmt.Errorf("upload response %d", response.StatusCode)
						return
					}
					received, e := strconv.ParseUint(string(body), 10, 64)
					if e != nil {
						errs <- e
						return
					}
					if count := <-sent; received != count {
						errs <- fmt.Errorf("upload truncation: received %d sent %d", received, count)
					}
				}()
			}
			if download {
				wg.Add(1)
				go func() {
					defer wg.Done()
					requestCtx := ctx
					stopRequest := func() {}
					if *cancelDownload > 0 {
						requestCtx, stopRequest = context.WithTimeout(ctx, *cancelDownload)
					}
					defer stopRequest()
					request, e := http.NewRequestWithContext(requestCtx, http.MethodGet, "https://fixture.example/download", nil)
					if e != nil {
						errs <- e
						return
					}
					response, e := httpClient.Do(request)
					if e != nil {
						errs <- e
						return
					}
					defer response.Body.Close()
					buffer := make([]byte, 64<<10)
					offset := uint64(0)
					for {
						n, e := response.Body.Read(buffer)
						if n > 0 {
							if !valid(buffer[:n], offset) {
								errs <- errors.New("download corruption")
								return
							}
							offset += uint64(n)
							down.add(n)
						}
						if e == io.EOF {
							return
						}
						if e != nil {
							if *cancelDownload > 0 && offset > 128<<10 && errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
								return
							}
							errs <- e
							return
						}
					}
				}()
			}
		}
		finished := make(chan time.Time, 1)
		go func() { wg.Wait(); finished <- time.Now() }()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case completed := <-finished:
				close(errs)
				var joined error
				for e := range errs {
					joined = errors.Join(joined, e)
				}
				ur, dr := up.result(), down.result()
				elapsed := completed.Sub(begin).Seconds()
				ur["full_phase_mbps"] = float64(ur["verified_bytes"].(uint64)) * 8 / elapsed / 1e6
				dr["full_phase_mbps"] = float64(dr["verified_bytes"].(uint64)) * 8 / elapsed / 1e6
				r.Phases = append(r.Phases, map[string]any{"name": name, "streams": *streams, "elapsed_seconds": elapsed, "send_seconds": duration.Seconds(), "upload": ur, "download": dr, "success": joined == nil})
				return joined
			case <-tick.C:
				ms, e := ping()
				if e != nil {
					if file, openErr := os.Create(filepath.Join(filepath.Dir(*out), *label+"-failure-goroutines.txt")); openErr == nil {
						_ = pprof.Lookup("goroutine").WriteTo(file, 2)
						file.Close()
					}
					return fmt.Errorf("loaded ping: %w", e)
				}
				r.LoadedMS = append(r.LoadedMS, ms)
			}
		}
	}
	for cycle := 0; cycle < *cycles; cycle++ {
		transport.CloseIdleConnections() // Each speedtest opens fresh TLS.
		if *duplex {
			err = phase(true, true)
		} else {
			if err = phase(true, false); err == nil {
				err = phase(false, true)
			}
		}
		if err != nil {
			return err
		}
	}
	r.Stage = "post_load"
	// A pre-existing chat stream alone cannot prove that new browser/TLS
	// connections still work after the speedtest resets its bulk streams.
	freshTransport := transport.Clone()
	defer freshTransport.CloseIdleConnections()
	freshResponse, freshErr := (&http.Client{Transport: freshTransport, Timeout: 5 * time.Second}).Get("https://fixture.example/health")
	if freshErr != nil {
		return fmt.Errorf("fresh HTTPS after load: %w", freshErr)
	}
	freshBody, freshErr := io.ReadAll(io.LimitReader(freshResponse.Body, 128))
	freshResponse.Body.Close()
	if freshErr != nil || string(freshBody) != "fresh HTTPS works" {
		return fmt.Errorf("fresh HTTPS body after load: %q %v", freshBody, freshErr)
	}
	if err = mark(); err != nil {
		return err
	}
	for i := 0; i < *postProbes; i++ {
		ms, e := ping()
		if e != nil {
			return e
		}
		r.AfterMS = append(r.AfterMS, ms)
	}
	response, e := http.Get(p.Metrics)
	if e == nil {
		defer response.Body.Close()
		_ = json.NewDecoder(response.Body).Decode(&r.Provider)
	}
	r.Stage = "complete"
	r.Success = true
	r.Targets = targets(r)
	return nil
}

func fill(body []byte, offset uint64) {
	for i := range body {
		at := offset + uint64(i)
		body[i] = byte((at*131 + (at>>8)*17 + 29) % 251)
	}
}
func valid(body []byte, offset uint64) bool {
	for i, b := range body {
		at := offset + uint64(i)
		if b != byte((at*131+(at>>8)*17+29)%251) {
			return false
		}
	}
	return true
}

func targets(r result) map[string]bool {
	t := map[string]bool{"idle_and_light_rtt": true, "loaded_rtt": true, "full_phase_100mbps": true, "ramp_100mbps_within_2s": true, "rolling_5s_100mbps": true, "no_half_second_stalls": true}
	for _, ms := range append(append([]float64(nil), r.IdleMS...), r.AfterMS...) {
		if ms > 300 {
			t["idle_and_light_rtt"] = false
		}
	}
	for _, ms := range r.LoadedMS {
		if ms > 2000 {
			t["loaded_rtt"] = false
		}
	}
	for _, phase := range r.Phases {
		for _, direction := range []string{"upload", "download"} {
			if phase["name"] != direction && phase["name"] != "duplex" {
				continue
			}
			value := phase[direction].(map[string]any)
			if value["full_phase_mbps"].(float64) < 100 {
				t["full_phase_100mbps"] = false
			}
			rates := value["half_second_mbps"].([]float64)
			if value["max_receiver_gap_after_ramp_ms"].(float64) >= 500 {
				t["no_half_second_stalls"] = false
			}
			for i, rate := range rates {
				if i >= 4 && float64(i+1)*.5 <= phase["send_seconds"].(float64) && rate == 0 {
					t["no_half_second_stalls"] = false
				}
			}
			reached := false
			for _, rate := range rates[:min(4, len(rates))] {
				if rate >= 100 {
					reached = true
				}
			}
			if !reached {
				t["ramp_100mbps_within_2s"] = false
			}
			for start := 4; start+10 <= len(rates) && float64(start+10)*.5 <= phase["elapsed_seconds"].(float64); start++ {
				sum := 0.0
				for _, rate := range rates[start : start+10] {
					sum += rate
				}
				if sum/10 < 100 {
					t["rolling_5s_100mbps"] = false
				}
			}
		}
	}
	return t
}
