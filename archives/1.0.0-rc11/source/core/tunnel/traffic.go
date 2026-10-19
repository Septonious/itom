package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"itom.local/core/datapath"
	"itom.local/core/servercfg"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const TrafficFile = "traffic.json"

// Useful TCP payload at the server exit, excluding IMAP/Noise overhead and
// retransmission. Download counts bytes read from the exit; upload counts
// successful writes to it. It does not assert remote application consumption.
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
type userTraffic struct {
	upload, download atomic.Uint64
	sockets          atomic.Int64
}
type trafficSocket struct {
	datapath.Socket
	stats  *userTraffic
	closed sync.Once
}

func (s *trafficSocket) Read(b []byte) (int, error) {
	n, err := s.Socket.Read(b)
	if n > 0 {
		s.stats.download.Add(uint64(n))
	}
	return n, err
}
func (s *trafficSocket) Write(b []byte) (int, error) {
	n, err := s.Socket.Write(b)
	if n > 0 {
		s.stats.upload.Add(uint64(n))
	}
	return n, err
}
func (s *trafficSocket) Close() error {
	err := s.Socket.Close()
	s.closed.Do(func() { s.stats.sockets.Add(-1) })
	return err
}
func (s *userTraffic) wrap(socket datapath.Socket) datapath.Socket {
	if s == nil {
		return socket
	}
	s.sockets.Add(1)
	return &trafficSocket{Socket: socket, stats: s}
}

type serverTraffic struct {
	users    map[string]*userTraffic
	order    []string
	started  time.Time
	instance string
}

func newServerTraffic(cfg servercfg.Config) *serverTraffic {
	now := time.Now()
	t := &serverTraffic{users: map[string]*userTraffic{}, started: now, instance: fmt.Sprintf("%d-%d", os.Getpid(), now.UnixNano())}
	for _, u := range cfg.Users {
		t.users[u.ID] = &userTraffic{}
		t.order = append(t.order, u.ID)
	}
	return t
}
func (t *serverTraffic) snapshot(running bool) TrafficSnapshot {
	s := TrafficSnapshot{Instance: t.instance, StartedAtMS: t.started.UnixMilli(), SampledAtMS: time.Now().UnixMilli(), Running: running, Users: []UserTraffic{}}
	for _, id := range t.order {
		u := t.users[id]
		s.Users = append(s.Users, UserTraffic{ID: id, Upload: u.upload.Load(), Download: u.download.Load(), Sockets: u.sockets.Load()})
	}
	return s
}
func writeTraffic(path string, s TrafficSnapshot) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".traffic-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// This is a transient atomic snapshot, not a durable quota journal. No
	// fsync is added to the TCP/mailbox hot path.
	return os.Rename(f.Name(), path)
}
func (t *serverTraffic) publish(ctx context.Context, path string) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = writeTraffic(path, t.snapshot(true))
		}
	}
}
func ReadTraffic(path string) (TrafficSnapshot, error) {
	var s TrafficSnapshot
	stat, err := os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 8192 {
		return s, errors.New("TRAFFIC_SNAPSHOT_INVALID")
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 8193))
	if err = d.Decode(&s); err != nil {
		return s, err
	}
	if d.Decode(new(any)) != io.EOF || s.Instance == "" || s.StartedAtMS <= 0 || s.SampledAtMS < s.StartedAtMS || len(s.Users) > 2 {
		return s, errors.New("TRAFFIC_SNAPSHOT_INVALID")
	}
	if time.Since(time.UnixMilli(s.SampledAtMS)) > 5*time.Second {
		s.Running = false
	}
	return s, nil
}
