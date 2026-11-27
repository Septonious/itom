package mailstream

import (
	"bytes"
	"context"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type rc7CountControlCreates struct {
	imapserver.Session
	creates *atomic.Int32
}

func (s rc7CountControlCreates) Create(name string, o *imap.CreateOptions) error {
	if bootstrapControl(name) {
		s.creates.Add(1)
	}
	return s.Session.Create(name, o)
}
func TestRC7ManagedClientNeverCreatesSharedControlFolders(t *testing.T) {
	var creates atomic.Int32
	accounts, _ := applicationMailServerAccounts(t, true, false, func(s imapserver.Session) imapserver.Session { return rc7CountControlCreates{s, &creates} }, nil, 3)
	for i := range accounts {
		accounts[i].ServerCreatesControls = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cli, e := NewIMAPLink(ctx, "rc7-single-creator", accounts, true)
	if e != nil {
		t.Fatal(e)
	}
	defer cli.Close()
	waitRetention(t, ctx, func() bool {
		for _, a := range cli.Snapshot().Lines {
			if a.Stage != "control_wait_server" {
				return false
			}
		}
		return true
	}, "client waits for server ownership")
	if creates.Load() != 0 {
		t.Fatal("client created shared mailbox")
	}
	srv, e := NewIMAPLink(ctx, "rc7-single-creator", accounts, false)
	if e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	key := bytes.Repeat([]byte{5}, 32)
	go Serve(ctx, srv, "rc7-single-creator", key, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	}, nil)
	s, e := Connect(ctx, cli, "rc7-single-creator", key)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	waitRetention(t, ctx, func() bool { return cli.Snapshot().Data == 3 && srv.Snapshot().Data == 3 }, "all accounts attached")
	for _, a := range cli.Snapshot().Lines {
		if a.StartupAttempts != 1 {
			t.Fatalf("redundant startup login: %s attempts=%d", a.Account, a.StartupAttempts)
		}
	}
	if creates.Load() != 6 {
		t.Fatalf("wanted exactly 6 control creates, got %d", creates.Load())
	}
	if _, e = s.Ping(ctx); e != nil {
		t.Fatal(e)
	}
}
