package adminapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProofBindsChallengeAndExactCommand(t *testing.T) {
	key := strings.Repeat("12", 32)
	nonce := strings.Repeat("34", 16)
	raw := []byte(`{"operation":"service_stop"}`)
	a, e := Proof(key, nonce, raw)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := Proof(key, strings.Repeat("35", 16), raw)
	c, _ := Proof(key, nonce, []byte(`{"operation":"service_start"}`))
	if a == b || a == c {
		t.Fatal("proof did not bind command and one-use challenge")
	}
	if _, e = Proof("not-a-key", nonce, raw); e == nil {
		t.Fatal("bad key accepted")
	}
	if _, e = Proof(key, "bad", raw); e == nil {
		t.Fatal("bad challenge accepted")
	}
}
func TestCallUsesProvidedStreamAndCancellationClosesIt(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	defer server.Close()
	go func() {
		defer close(done)
		_, _ = io.WriteString(server, "ITOM-ADMIN-1 "+strings.Repeat("a", 32)+"\n")
		r, e := http.ReadRequest(bufio.NewReader(server))
		if e != nil {
			return
		}
		defer r.Body.Close()
		_, _ = io.ReadAll(r.Body)
		var b [1]byte
		_, _ = server.Read(b[:])
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := Call(ctx, client, []byte(`{"operation":"status"}`), strings.Repeat("1", 64)); e == nil {
		t.Fatal("unanswered request succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation failed to close stream")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("peer was not released")
	}
}
func TestTypedRequestRejectsUnknownFieldsAndOversize(t *testing.T) {
	for _, raw := range []string{`{"operation":"status","shell":"rm"}`, `{"operation":"status"} {}`, strings.Repeat("x", MaxRequest+1)} {
		if _, e := DecodeRequest([]byte(raw)); e == nil {
			t.Fatal("unbounded or untyped request accepted")
		}
	}
	b, _ := json.Marshal(Request{Operation: "status"})
	if _, e := DecodeRequest(b); e != nil {
		t.Fatal(e)
	}
}
