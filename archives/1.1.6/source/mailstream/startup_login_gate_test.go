package mailstream

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type stalledStartupMetadataSession struct {
	imapserver.Session
	entered chan<- struct{}
	release <-chan struct{}
	user    string
	all     bool
}

func (s *stalledStartupMetadataSession) Login(user, password string) error {
	s.user = user
	return s.Session.Login(user, password)
}

func (s *stalledStartupMetadataSession) Create(name string, options *imap.CreateOptions) error {
	if (s.all || s.user != "fixture0") && (strings.HasSuffix(name, "-c-d") || strings.HasSuffix(name, "-c-u")) {
		select {
		case s.entered <- struct{}{}:
		default:
		}
		<-s.release
	}
	return s.Session.Create(name, options)
}

// A provider CREATE that stalls for 15s must not occupy a scarce LOGIN permit.
// That same permit is needed to open DATA sockets when a healthy account
// attempts authenticated session attachment. Keep metadata concurrency bounded
// separately rather than disabling either limit.
func TestSlowControlCreateDoesNotHoldLoginPermit(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	defer releaseAll()
	accounts, _ := applicationMailServerAccounts(t, false, false, func(s imapserver.Session) imapserver.Session {
		return &stalledStartupMetadataSession{Session: s, entered: entered, release: release, all: true}
	}, nil, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	l := &IMAPLink{ctx: ctx, identity: "slow-metadata-login-gate", client: true, login: make(chan struct{}, 1), metadata: make(chan struct{}, 1), changed: make(chan struct{})}
	a := &mailAccountState{link: l, config: accounts[0], ctx: ctx}
	result := make(chan error, 1)
	go func() {
		control, writer, err := a.openControls()
		if control != nil {
			control.close()
		}
		if writer != nil {
			writer.close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatalf("metadata CREATE never began: %v", ctx.Err())
	}
	if len(l.metadata) != 1 {
		t.Fatal("metadata permit was not held during CREATE")
	}
	// This would block before the fix, when the metadata operation held the
	// login semaphore for its entire provider deadline.
	select {
	case l.login <- struct{}{}:
		<-l.login
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stalled provider CREATE monopolized a LOGIN slot")
	}
	// Release the deliberately stalled metadata operation before cleanup.
	releaseAll()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("released mailbox setup failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("released mailbox setup did not finish")
	}
}

// Reproduces the incident's shape: an established set of healthy control
// accounts receives the Noise handshake while four other accounts are blocked
// in CREATE. DATA writers must be able to log in without waiting 15 seconds for
// those unrelated missing-mailbox operations to time out.
func TestBusyControlCreatesDoNotBlockHealthySessionAttachment(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	defer releaseAll()
	accounts, _ := applicationMailServerAccounts(t, false, false, func(s imapserver.Session) imapserver.Session {
		return &stalledStartupMetadataSession{Session: s, entered: entered, release: release}
	}, nil, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	server, err := NewIMAPLink(ctx, "metadata-attach", accounts[:1], false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "metadata-attach", accounts[:1], true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	// First allow the known-good account to pass metadata setup.
	for server.Snapshot().Ready != 1 || client.Snapshot().Ready != 1 {
		select {
		case <-ctx.Done():
			t.Fatalf("good account failed to enter service: server=%d client=%d", server.Snapshot().Ready, client.Snapshot().Ready)
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Simulate four additional accounts stalled at CREATE; they share the
	// server's admission gates but do not participate in the active link.
	done := make(chan struct{}, 4)
	for i := 1; i < 5; i++ {
		account := accounts[i]
		go func() {
			a := &mailAccountState{link: server, config: account, ctx: ctx}
			pipe, _, _ := a.openControls()
			if pipe != nil {
				pipe.close()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("four metadata stalls did not materialize", ctx.Err())
		}
	}
	if len(server.metadata) != 4 {
		t.Fatal("missing four simultaneous metadata leases")
	}
	key := bytes.Repeat([]byte{7}, 32)
	go Serve(ctx, server, "metadata-attach", key, (&net.Dialer{}).DialContext, nil)
	startup, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	session, err := Connect(startup, client, "metadata-attach", key)
	if err != nil {
		t.Fatalf("a healthy account failed session attachment while four other metadata CREATEs were stalled: %v", err)
	}
	session.Close()
	if server.Snapshot().Ready != 1 {
		t.Fatal("metadata stalls changed healthy ready-account count")
	}
	// No background fixture goroutines should survive test cleanup.
	releaseAll()
	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("metadata fixture did not finish")
		}
	}
}
