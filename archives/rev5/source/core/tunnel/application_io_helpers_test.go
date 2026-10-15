package tunnel

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"itom.local/core/wire"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Shared byte-integrity and TCP-target helpers for the public supervisor tests.
// The obsolete direct-lane load runner was removed in Rev4.
func loadEnvInt(t *testing.T, name string, def, minValue, maxValue int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		t.Fatalf("%s must be an integer in [%d,%d]", name, minValue, maxValue)
	}
	return value
}

func loadEnvFloat(t *testing.T, name string, def float64) float64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		t.Fatalf("%s must be a non-negative number", name)
	}
	return value
}

type productionLoadTarget struct {
	chunkBytes                         int
	chunkPause                         time.Duration
	measurement                        atomic.Pointer[applicationByteClock]
	test                               *testing.T
	identity                           string
	echoListener                       net.Listener
	startCh                            chan struct{}
	startOnce                          sync.Once
	uploadDelivered, downloadDelivered atomic.Int64
	stopped                            atomic.Bool
	errors                             chan error
	mu                                 sync.Mutex
	connections                        []net.Conn
}

func newProductionLoadTarget(t *testing.T, identity string) *productionLoadTarget {
	target := &productionLoadTarget{test: t, identity: identity, startCh: make(chan struct{}), errors: make(chan error, 256)}
	target.chunkBytes = loadEnvInt(t, "ITOM_LOAD_CHUNK_BYTES", 32<<10, 512, 64<<10)
	target.chunkPause = time.Duration(loadEnvInt(t, "ITOM_LOAD_CHUNK_PAUSE_MS", 0, 0, 100)) * time.Millisecond
	target.echoListener = target.listen()
	t.Cleanup(target.close)
	go target.runEcho()
	return target
}
func (t *productionLoadTarget) listen() net.Listener {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.test.Fatal(err)
	}
	t.test.Cleanup(func() { _ = l.Close() })
	return l
}
func (t *productionLoadTarget) close() {
	t.stopped.Store(true)
	t.startBulk()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.connections {
		_ = c.Close()
	}
}
func (t *productionLoadTarget) track(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped.Load() {
		_ = c.Close()
		return
	}
	t.connections = append(t.connections, c)
}
func (t *productionLoadTarget) startBulk() { t.startOnce.Do(func() { close(t.startCh) }) }
func (t *productionLoadTarget) connect(l net.Listener) wire.Connect {
	_, port, _ := net.SplitHostPort(l.Addr().String())
	n, _ := strconv.Atoi(port)
	return wire.Connect{Host: "127.0.0.1", Port: uint16(n)}
}
func (t *productionLoadTarget) echoConnect() wire.Connect { return t.connect(t.echoListener) }
func (t *productionLoadTarget) streamIdentity(upload bool, stream int) string {
	direction := "download"
	if upload {
		direction = "upload"
	}
	return fmt.Sprintf("%s/%s/%d", t.identity, direction, stream)
}

// Each target port has an independently known expected stream identity. A
// swapped entire stream (including any self-declared header) cannot validate.
func (t *productionLoadTarget) bulkConnect(upload bool, stream int) wire.Connect {
	l := t.listen()
	identity := t.streamIdentity(upload, stream)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		t.track(c)
		// A reconnect must preserve this target TCP stream. Observe any second
		// dial to the stream's dedicated port instead of leaving it in backlog.
		go func() {
			for {
				duplicate, err := l.Accept()
				if err != nil {
					return
				}
				duplicate.Close()
				if !t.stopped.Load() {
					t.report(fmt.Errorf("%s: duplicate target TCP dial", identity))
				}
			}
		}()
		generator := newProductionPattern(identity)
		if upload {
			buf := make([]byte, 64<<10)
			expected := make([]byte, len(buf))
			for {
				n, err := c.Read(buf)
				if n > 0 {
					if e := generator.verify(buf[:n], expected); e != nil {
						t.report(fmt.Errorf("%s: %w", identity, e))
						return
					}
					t.uploadDelivered.Add(int64(n))
					t.recordWindow(true, n)
				}
				if err != nil {
					if !t.stopped.Load() {
						t.report(fmt.Errorf("%s: unexpected active-stream termination: %w", identity, err))
					}
					return
				}
			}
		}
		<-t.startCh
		chunk := make([]byte, t.chunkBytes)
		for !t.stopped.Load() {
			if t.chunkPause > 0 {
				time.Sleep(t.chunkPause)
			}
			generator.fill(chunk)
			if err := productionWriteFull(c, chunk); err != nil {
				if !t.stopped.Load() {
					t.report(fmt.Errorf("%s: target write: %w", identity, err))
				}
				return
			}
		}
	}()
	return t.connect(l)
}
func (t *productionLoadTarget) report(err error) {
	if err == nil {
		return
	}
	select {
	case t.errors <- err:
	default:
	}
}
func (t *productionLoadTarget) firstError() error {
	select {
	case err := <-t.errors:
		return err
	default:
		return nil
	}
}
func (t *productionLoadTarget) runEcho() {
	for {
		c, err := t.echoListener.Accept()
		if err != nil {
			return
		}
		t.track(c)
		go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
	}
}

func productionWriteFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

type productionPattern struct {
	state uint64
	word  [8]byte
	used  int
}

func newProductionPattern(identity string) *productionPattern {
	digest := sha256.Sum256([]byte(identity))
	seed := binary.LittleEndian.Uint64(digest[:8])
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &productionPattern{state: seed, used: 8}
}

func (p *productionPattern) refill() {
	x := p.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	p.state = x
	binary.LittleEndian.PutUint64(p.word[:], x*0x2545f4914f6cdd1d)
	p.used = 0
}

func (p *productionPattern) fill(dst []byte) {
	for len(dst) > 0 {
		if p.used == len(p.word) {
			p.refill()
		}
		n := copy(dst, p.word[p.used:])
		p.used += n
		dst = dst[n:]
	}
}

func (p *productionPattern) verify(data, scratch []byte) error {
	if len(scratch) < len(data) {
		return fmt.Errorf("verification scratch too small")
	}
	expected := scratch[:len(data)]
	p.fill(expected)
	if !bytes.Equal(data, expected) {
		return fmt.Errorf("content mismatch")
	}
	return nil
}

func productionPercentile(samples []time.Duration, q float64) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*q)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
