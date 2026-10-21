package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/wire"
)

// Phase changes and cancelled HTTP transfers match the field application
// lifecycle. Each response and upload is independently verified at the target;
// connected IMAP lanes or successful OPEN alone never count as recovery.
func TestRev4HTTPPhasesKeepMessagesUsable(t *testing.T) {
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", "240")
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", "40")
	f := rev4FieldFixture(t, true)
	var verifiedUploads atomic.Int64
	pattern := func(seed, size int) []byte {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte((i*37 + seed*71) % 251)
		}
		return data
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seed, _ := strconv.Atoi(r.URL.Query().Get("seed"))
		size := 256 << 10
		if r.URL.Path == "/message" {
			size = 4096
		}
		want := pattern(seed, size)
		if r.Method == http.MethodPost {
			got, err := io.ReadAll(io.LimitReader(r.Body, int64(size+1)))
			if err != nil || !bytes.Equal(got, want) {
				http.Error(w, "upload integrity", http.StatusBadRequest)
				return
			}
			verifiedUploads.Add(1)
			digest := sha256.Sum256(got)
			_, _ = io.WriteString(w, hex.EncodeToString(digest[:]))
			return
		}
		_, _ = w.Write(want)
	}))
	defer srv.Close()
	transport := &http.Transport{MaxConnsPerHost: 24, MaxIdleConnsPerHost: 24, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		p, _ := strconv.Atoi(port)
		stream, _, err := f.client.Open(ctx, wire.Connect{Host: host, Port: uint16(p)})
		if err != nil {
			return nil, err
		}
		return stream.(net.Conn), nil
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	request := func(ctx context.Context, method, path string, seed int) error {
		size := 256 << 10
		if path == "/message" {
			size = 4096
		}
		want := pattern(seed, size)
		var body io.Reader
		if method == http.MethodPost {
			body = bytes.NewReader(want)
		}
		req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("%s%s?seed=%d", srv.URL, path, seed), body)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		got, err := io.ReadAll(io.LimitReader(response.Body, int64(size+1)))
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP status %d", response.StatusCode)
		}
		if method == http.MethodPost {
			sum := sha256.Sum256(want)
			want = []byte(hex.EncodeToString(sum[:]))
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("HTTP application integrity")
		}
		return nil
	}
	for round, method := range []string{http.MethodGet, http.MethodPost, http.MethodGet} {
		var wg sync.WaitGroup
		failures := make(chan error, 12)
		start := time.Now()
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := request(f.ctx, method, "/bulk", round*100+i); err != nil {
					failures <- fmt.Errorf("%s stream %d: %w", method, i, err)
				}
			}(i)
		}
		for i := 0; i < 4; i++ {
			ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
			err := request(ctx, http.MethodGet, "/message", 1000+round*10+i)
			cancel()
			if err != nil {
				t.Errorf("message unavailable in phase %s: %v", method, err)
				break
			}
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		t.Logf("field_http_phase method=%s useful_bytes=%d elapsed_ms=%d", method, 12*(256<<10), time.Since(start).Milliseconds())
		if t.Failed() {
			return
		}
	}
	if verifiedUploads.Load() != 12 {
		t.Fatal("upload requests never reached target", verifiedUploads.Load())
	}
	if err := request(f.ctx, http.MethodGet, "/message", 2000); err != nil {
		t.Fatal("message unavailable after phase changes", err)
	}
}
