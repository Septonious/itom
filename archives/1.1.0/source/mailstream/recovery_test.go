package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Real HTTPS creates partial server-produced DATA segments. Restart both
// transport endpoints with their private state intact, then require verified
// deletion to recover the old charged capacity while the new session works.
func TestHTTPSRestartReclaimsHistoricalServerData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	serverState, clientState := t.TempDir(), t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 1_234_567)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer origin.Close()
	var recovered int
	var preserved []string
	for cycle := 0; cycle < 2; cycle++ {
		server, err := NewStoredIMAPLink(ctx, "recovery", accounts, false, serverState)
		if err != nil {
			t.Fatal(err)
		}
		if cycle == 1 && server.Snapshot().RecoveredBytes != recovered {
			t.Fatal("restart reset historical debt")
		}
		client, err := NewStoredIMAPLink(ctx, "recovery", accounts, true, clientState)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, server, "recovery", key, (&net.Dialer{}).DialContext, nil) }()
		session, err := Connect(ctx, client, "recovery", key)
		if err != nil {
			client.Close()
			server.Close()
			t.Fatal(err)
		}
		transport := origin.Client().Transport.(*http.Transport).Clone()
		transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
		transport.ForceAttemptHTTP2 = false
		if cycle == 0 {
			// Exercise actual provider mailbox boundaries: a collector must
			// preserve legacy, another identity and client-produced DATA.
			preserved = []string{"legacy-wallet-mail", "itom3-other-01010101010101010101010101010101-s2c-00000000", server.segmentFolder(session.cipher.id, false, 0)}
			seed, err := openMailPipe(ctx, accounts[0])
			if err != nil {
				t.Fatal(err)
			}
			for _, folder := range preserved {
				_ = seed.client.Create(folder, nil).Wait()
			}
			seed.close()
			response, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Get(origin.URL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatalf("HTTPS integrity: %v", err)
			}
			for _, line := range client.Snapshot().Lines {
				if line.RTTMillis <= 0 || line.IMAPLatencyMillis <= 0 || line.DownloadBytes == 0 {
					t.Fatalf("account metrics missing: %+v", line)
				}
			}
		} else {
			deadline := time.Now().Add(8 * time.Second)
			for server.Snapshot().RetainedBytes >= recovered/4 && time.Now().Before(deadline) {
				if _, err := session.Ping(ctx); err != nil {
					t.Fatal("recovery blocked live session", err)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if snapshot := server.Snapshot(); snapshot.RetainedBytes >= recovered/4 || snapshot.Failed != 0 {
				t.Fatalf("historical capacity not recovered: before=%d after=%+v", recovered, snapshot)
			}
			probe, err := openMailPipe(ctx, accounts[0])
			if err != nil {
				t.Fatal(err)
			}
			for _, folder := range preserved {
				rows, err := probe.client.List("", folder, nil).Collect()
				if err != nil || len(rows) != 1 {
					probe.close()
					t.Fatalf("collector removed protected namespace %q: %v", folder, err)
				}
			}
			probe.close()
		}
		transport.CloseIdleConnections()
		session.Close()
		client.Close()
		server.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("server did not stop")
		}
		recovered = server.Snapshot().RetainedBytes
		if cycle == 0 && recovered < len(payload) {
			t.Fatal("fixture did not retain partial outbound segments")
		}
	}
}
