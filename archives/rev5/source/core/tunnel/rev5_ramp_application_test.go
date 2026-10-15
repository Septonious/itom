package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"itom.local/core/wire"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The router-reset field capture has successful website bulk after several
// failed app ramps. Exercise increasing fanout, cancellation during delivery,
// three consecutive ramps, then an intact next transfer on the same session.
// A retained chat socket must stay usable throughout every ramp and cleanup.
func TestRev5RampAbortAndRestartKeepOriginalChatUsable(t *testing.T) {
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	t.Setenv("ITOM_LOAD_DATA_SHARDS", "4")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", "240")
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", "40")
	f := newProductionSupervisorFixtureState(t, 15, rev4RetainedState(t))
	f.network.setMbps(40)
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 900, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("initial chat", err)
	}
	payload := make([]byte, 512<<10)
	for i := range payload {
		payload[i] = byte((i*131 + 17) % 251)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		// Small initial server writes become a sustained download after setup.
		for pos := 0; pos < len(payload); pos += 16 << 10 {
			if _, err := w.Write(payload[pos : pos+(16<<10)]); err != nil {
				return
			}
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
		}
	}))
	defer srv.Close()
	tr := &http.Transport{MaxConnsPerHost: 24, MaxIdleConnsPerHost: 24, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		p, _ := strconv.Atoi(port)
		c, _, err := f.client.Open(ctx, wire.Connect{Host: host, Port: uint16(p)})
		if err != nil {
			return nil, err
		}
		return c.(net.Conn), nil
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	transfer := func(abort bool) error {
		res, err := client.Get(srv.URL)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		size := len(payload)
		if abort {
			size = 128 << 10
		}
		got, err := io.ReadAll(io.LimitReader(res.Body, int64(size)))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, payload[:size]) {
			return fmt.Errorf("download integrity abort=%t bytes=%d", abort, len(got))
		}
		return nil
	}
	for round := range 3 {
		for _, fanout := range []int{1, 4, 12} {
			var wg sync.WaitGroup
			failures := make(chan error, fanout)
			started := time.Now()
			for i := range fanout {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if err := transfer(i%2 == 0); err != nil {
						failures <- err
					}
				}(i)
			}
			// Reuse the already established messaging socket, so OPEN cannot hide a
			// queued DATA/receipt problem. Every response has an independent deadline.
			for probe := range 3 {
				probeStarted := time.Now()
				if err := rev4Exchange(chat, 1000+round*100+fanout+probe, 4096, 1100*time.Millisecond); err != nil {
					rev4RecoveryFailure(t, f)
					t.Errorf("chat during ramp round=%d fanout=%d: %v", round, fanout, err)
					break
				}
				t.Logf("ramp_chat round=%d fanout=%d probe=%d rtt_us=%d", round, fanout, probe, time.Since(probeStarted).Microseconds())
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				t.Error("ramp request", round, fanout, err)
			}
			t.Logf("ramp round=%d fanout=%d elapsed_ms=%d verified=true", round, fanout, time.Since(started).Milliseconds())
			if t.Failed() {
				return
			}
		}
	}
	if err := transfer(false); err != nil {
		t.Fatal("intact website transfer after aborted ramps", err)
	}
	if err := rev4Exchange(chat, 2000, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("original chat after ramps", err)
	}
}
