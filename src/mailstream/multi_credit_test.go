package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// All supported stream slots transfer more than the shared application budget
// in aggregate. Coalesced credit must preserve progress, integrity and cancellation
// without a larger initial window or a larger receive-memory allowance.
func TestSixtyFourTLSStreamsReturnSharedCredit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	server, err := NewStoredIMAPLink(ctx, "multi-credit", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewStoredIMAPLink(ctx, "multi-credit", accounts, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := bytes.Repeat([]byte{17}, 32)
	go Serve(ctx, server, "multi-credit", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "multi-credit", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	body := make([]byte, 1<<20)
	if _, err = rand.Read(body); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		n, e := io.Copy(h, r.Body)
		if e != nil || n != int64(len(body)) || !bytes.Equal(h.Sum(nil), sum[:]) {
			http.Error(w, "upload integrity", 500)
			return
		}
		w.Write(body)
	}))
	defer origin.Close()
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.DisableKeepAlives = true
	tr.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	defer tr.CloseIdleConnections()
	h := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	var joined sync.WaitGroup
	errors := make(chan error, 64)
	for range 64 {
		joined.Add(1)
		go func() {
			defer joined.Done()
			response, e := h.Post(origin.URL, "application/octet-stream", bytes.NewReader(body))
			if e != nil {
				errors <- e
				return
			}
			defer response.Body.Close()
			digest := sha256.New()
			n, e := io.Copy(digest, response.Body)
			if e != nil || response.StatusCode != 200 || n != int64(len(body)) || !bytes.Equal(digest.Sum(nil), sum[:]) {
				errors <- fmt.Errorf("TLS stream integrity status=%d bytes=%d: %v", response.StatusCode, n, e)
			}
		}()
	}
	joined.Wait()
	close(errors)
	for e := range errors {
		t.Error(e)
	}
	if client.Snapshot().Failed != 0 || server.Snapshot().Failed != 0 {
		t.Fatal("concurrent TLS traffic lost a mail carrier")
	}
	waitRetention(t, ctx, func() bool { return session.Snapshot().Streams == 0 && session.Snapshot().ReceiveBuffered == 0 }, "closed concurrent TLS streams retained memory or credit")
}
