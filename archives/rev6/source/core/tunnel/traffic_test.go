package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type partialTrafficSocket struct{ net.Conn }

func (partialTrafficSocket) Read(b []byte) (int, error)  { return copy(b, []byte("download")), io.EOF }
func (partialTrafficSocket) Write(b []byte) (int, error) { return min(len(b), 3), io.ErrUnexpectedEOF }
func (partialTrafficSocket) Close() error                { return nil }
func (partialTrafficSocket) CloseWrite() error           { return nil }

func TestTrafficCountsUsefulBytesAndClosesExactlyOnce(t *testing.T) {
	stats := &userTraffic{}
	s := stats.wrap(partialTrafficSocket{})
	if stats.sockets.Load() != 1 {
		t.Fatal("missing socket")
	}
	if n, err := s.Write(make([]byte, 100)); n != 3 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(n, err)
	}
	if n, err := s.Read(make([]byte, 100)); n != 8 || !errors.Is(err, io.EOF) {
		t.Fatal(n, err)
	}
	_ = s.CloseWrite()
	if stats.sockets.Load() != 1 {
		t.Fatal("half close retired full socket")
	}
	_ = s.Close()
	_ = s.Close()
	if stats.upload.Load() != 3 || stats.download.Load() != 8 || stats.sockets.Load() != 0 {
		t.Fatal("partial I/O accounting or duplicate close")
	}
	other := &userTraffic{}
	if other.upload.Load() != 0 || other.download.Load() != 0 {
		t.Fatal("cross-user accounting")
	}
}
func TestTrafficSnapshotsArePrivateAndMarkStaleServer(t *testing.T) {
	cfg := servercfg.Config{Users: []servercfg.User{{ID: "12345678"}, {ID: "87654321"}}}
	stats := newServerTraffic(cfg)
	stats.users["12345678"].upload.Add(1_000_000)
	stats.users["87654321"].download.Add(2_000_000)
	p := filepath.Join(t.TempDir(), TrafficFile)
	if err := writeTraffic(p, stats.snapshot(true)); err != nil {
		t.Fatal(err)
	}
	s, err := ReadTraffic(p)
	if err != nil || len(s.Users) != 2 || s.Users[0].Upload != 1_000_000 || s.Users[1].Upload != 0 || !s.Running {
		t.Fatal(s, err)
	}
	st, err := os.Stat(p)
	if err != nil || runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatal("stats permissions", err)
	}
	s.StartedAtMS -= 10_000
	s.SampledAtMS -= 6_000
	if err := writeTraffic(p, s); err != nil {
		t.Fatal(err)
	}
	if s, err = ReadTraffic(p); err != nil || s.Running {
		t.Fatal("stale snapshot claimed server running", err)
	}
}
func TestServerTrafficIncludesEncryptedTransportWithLoggingDisabled(t *testing.T) {
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	t.Setenv("ITOM_LOAD_SERVER_DIAGNOSTICS_OFF", "1")
	f := newProductionSupervisorFixtureAccounts(t, 2)
	c, _, err := f.client.Open(f.ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(f.listener.Addr().(*net.TCPAddr).Port)})
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("payload"), 9000)
	// A simultaneous read allows net.Pipe-backed app sockets to drain.
	done := make(chan error, 1)
	go func() { _, err := c.Write(payload); done <- err }()
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(c, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatal("payload integrity", err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	path := filepath.Join(f.serverDir, TrafficFile)
	for {
		s, err := ReadTraffic(path)
		if err == nil && len(s.Users) == 1 && s.Users[0].Upload == uint64(len(payload)) && s.Users[0].Download == uint64(len(payload)) && s.Users[0].Sockets == 0 {
			if f.serverLogs.Load() != 0 {
				t.Fatal("stats enabled logs")
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("server counters did not match exact payload", s, err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	publisher := newServerTraffic(servercfg.Config{})
	donePub := make(chan struct{})
	go func() { defer close(donePub); publisher.publish(ctx, filepath.Join(t.TempDir(), TrafficFile)) }()
	cancel()
	select {
	case <-donePub:
	case <-time.After(time.Second):
		t.Fatal("publisher ignored cancellation")
	}
}
