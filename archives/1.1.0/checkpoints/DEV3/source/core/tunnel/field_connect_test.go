package tunnel

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

func TestFieldConnectFailureReportsStageBeforeHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := mailbox.Config{Host: "127.0.0.1", Port: port, TLS: "tls", Username: "private-user", Password: "private-password"}
	_, err = mailbox.Dial(context.Background(), cfg, []string{"itom-check"})
	if err == nil {
		t.Fatal("unexpected dial")
	}
	var events []string
	d := &diagnostic{log: func(s string) { events = append(events, s) }}
	d.beginConnect()
	d.connected(time.Now().Add(-15*time.Second), err)
	d.reconnect(err, time.Second)
	s := d.snapshot()
	if s.LastConnectStage != "tcp" || s.LastConnectReason != "refused" || s.LastError != "IMAP_CONNECT_FAILED" || s.LastOperationMS < 15000 {
		t.Fatalf("stage=%s reason=%s code=%s ms=%d", s.LastConnectStage, s.LastConnectReason, s.LastError, s.LastOperationMS)
	}
	all := strings.Join(events, "\n")
	if !strings.Contains(all, "event=imap_connect_failed stage=tcp reason=refused code=IMAP_CONNECT_FAILED duration_ms=") || strings.Contains(all, "private") || strings.Contains(all, "127.0.0.1") {
		t.Fatalf("unsafe or missing diagnostics: %s", all)
	}
	d.beginConnect()
	d.connected(time.Now(), nil)
	if s := d.snapshot(); s.LastConnectStage != "" || s.LastConnectReason != "" {
		t.Fatal("stale failure after success")
	}
}
