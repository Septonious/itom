package mailbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func testServer(t *testing.T, mode string, plus bool) (Config, *tls.Config) {
	return testServerWithSession(t, mode, plus, nil)
}
func testServerWithSession(t *testing.T, mode string, plus bool, wrap func(imapserver.Session) imapserver.Session) (Config, *tls.Config) {
	t.Helper()
	caps := imap.CapSet{imap.CapIMAP4rev1: {}}
	if plus {
		caps[imap.CapUIDPlus] = struct{}{}
	}
	return testServerWithCaps(t, mode, caps, wrap)
}

func testServerWithCaps(t *testing.T, mode string, caps imap.CapSet, wrap func(imapserver.Session) imapserver.Session, taps ...func(*imapserver.Conn)) (Config, *tls.Config) {
	t.Helper()
	ts := httptest.NewTLSServer(nil)
	cert := ts.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	ts.Close()
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("user", "secret"))
	srv := imapserver.New(&imapserver.Options{NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		for _, tap := range taps {
			tap(conn)
		}
		session := imapserver.Session(mem.NewSession())
		if wrap != nil {
			session = wrap(session)
		}
		return session, nil, nil
	}, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}, Caps: caps})
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if mode == "tls" {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close(); ln.Close() })
	return Config{Host: "127.0.0.1", Port: port, TLS: mode, Username: "user", Password: "secret"}, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
}
func TestIdleWaitWakesOnNewMessage(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapUIDPlus: {}}
	cfg, tc := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Ensure(ctx, "itom-idle"); err != nil {
		t.Fatal(err)
	}
	if !reader.IdleCapable() {
		t.Fatal("server advertised IDLE but client did not enable it")
	}
	rows, cur, err := reader.PageTail(ctx, "itom-idle", Cursor{}, true)
	if err != nil || len(rows) != 0 {
		t.Fatalf("initial tail rows=%d err=%v", len(rows), err)
	}
	type idleResult struct {
		notified bool
		err      error
	}
	done := make(chan idleResult, 1)
	go func() {
		notified, err := reader.IdleWait(ctx, "itom-idle", 2*time.Second)
		done <- idleResult{notified: notified, err: err}
	}()
	// Give IDLE time to receive its continuation before APPENDing from the peer.
	time.Sleep(50 * time.Millisecond)
	raw, err := Encode([]byte("idle-notify"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Append(ctx, "itom-idle", raw); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || !got.notified {
			t.Fatalf("IdleWait notified=%t err=%v", got.notified, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("IDLE did not wake on new mail")
	}
	rows, next, err := reader.PageTail(ctx, "itom-idle", cur, false)
	if err != nil || len(rows) != 1 || next == cur {
		t.Fatalf("tail after IDLE rows=%d cur=%+v next=%+v err=%v", len(rows), cur, next, err)
	}
}

func TestIdleWaitDoesNotLoseQueuedMailboxUpdate(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}
	cfg, tc := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ensure(ctx, "itom-idle"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PageTail(ctx, "itom-idle", Cursor{}, true); err != nil {
		t.Fatal(err)
	}
	// Model an EXISTS delivered in the narrow gap after an empty tail FETCH but
	// before IdleWait begins. The wake must not be discarded as "stale".
	c.updates <- struct{}{}
	start := time.Now()
	notified, err := c.IdleWait(ctx, "itom-idle", 2*time.Second)
	if err != nil || !notified {
		t.Fatalf("queued update notified=%t err=%v", notified, err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("queued mailbox update waited for audit timeout")
	}
}

func TestIdleWaitWakeInterruptsWithoutClosingReader(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}
	cfg, tc := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ensure(ctx, "itom-idle"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PageTail(ctx, "itom-idle", Cursor{}, true); err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	type result struct {
		notified bool
		kicked   bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		notified, kicked, err := c.IdleWaitWake(ctx, "itom-idle", 2*time.Second, wake)
		done <- result{notified: notified, kicked: kicked, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	wake <- struct{}{}
	select {
	case got := <-done:
		if got.err != nil || got.notified || !got.kicked {
			t.Fatalf("wake result notified=%t kicked=%t err=%v", got.notified, got.kicked, got.err)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("IDLE DONE wake took %v", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("IDLE wake did not interrupt wait")
	}
	// DONE must leave the same selected connection usable; a wake is not a reconnect.
	if _, _, err := c.PageTail(ctx, "itom-idle", Cursor{}, true); err != nil {
		t.Fatalf("reader unusable after IDLE wake: %v", err)
	}
}

func TestIdleWaitWakeConsumesQueuedKickBeforeEnteringIdle(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}
	cfg, tc := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ensure(ctx, "itom-idle"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PageTail(ctx, "itom-idle", Cursor{}, true); err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	start := time.Now()
	notified, kicked, err := c.IdleWaitWake(ctx, "itom-idle", 2*time.Second, wake)
	if err != nil || notified || !kicked {
		t.Fatalf("queued wake notified=%t kicked=%t err=%v", notified, kicked, err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("queued kick entered IDLE for %v", elapsed)
	}
}

func TestIdleWaitAuditTimeoutDoesNotPoisonConnection(t *testing.T) {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}
	cfg, tc := testServerWithCaps(t, "tls", caps, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-idle"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ensure(ctx, "itom-idle"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PageTail(ctx, "itom-idle", Cursor{}, true); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	notified, err := c.IdleWait(ctx, "itom-idle", 75*time.Millisecond)
	if err != nil || notified {
		t.Fatalf("audit wait notified=%t err=%v", notified, err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("IDLE audit timeout returned too early")
	}
	// The DONE/Wait path must leave the same authenticated connection usable.
	if _, _, err := c.PageTail(ctx, "itom-idle", Cursor{}, true); err != nil {
		t.Fatalf("connection unusable after IDLE timeout: %v", err)
	}
}

func TestTLSIMAPAppendFetchVerifiedDelete(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		for _, plus := range []bool{true, false} {
			t.Run(mode+strconv.FormatBool(plus), func(t *testing.T) {
				cfg, tc := testServer(t, mode, plus)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				c, e := dial(ctx, cfg, []string{"itom-test"}, tc)
				if e != nil {
					t.Fatal(e)
				}
				defer c.Close()
				if e = c.Ensure(ctx, "itom-test"); e != nil {
					t.Fatal(e)
				}
				want := bytes.Repeat([]byte{0, 1, 2, 255}, 1024)
				raw, e := Encode(want)
				if e != nil {
					t.Fatal(e)
				}
				if e = c.Append(ctx, "itom-test", raw); e != nil {
					t.Fatal(e)
				}
				msgs, _, e := c.Page(ctx, "itom-test", Cursor{})
				if e != nil || len(msgs) != 1 {
					t.Fatal(len(msgs), e)
				}
				got, e := Decode(msgs[0].MIME)
				if e != nil || !bytes.Equal(got, want) {
					t.Fatal(e)
				}
				bad := msgs[0]
				bad.Validity++
				if e = c.Delete(ctx, bad); e != ErrChanged {
					t.Fatal(e)
				}
				// UIDVALIDITY mismatch is a semantic proof failure, not a poisoned
				// transport connection. The same authenticated IMAP session must
				// remain usable for a fresh, correctly scoped operation.
				if e = c.Delete(ctx, msgs[0]); e != nil {
					t.Fatal(e)
				}
				after, _, e := c.Page(ctx, "itom-test", Cursor{})
				if e != nil || len(after) != 0 {
					t.Fatal(after, e)
				}
				absent, err := c.Absent(ctx, "itom-test", msgs[0].Validity, msgs[0].UID)
				if err != nil || !absent {
					t.Fatal("missing cleanup proof", absent, err)
				}
				if e = c.Append(ctx, "INBOX", raw); e != ErrScope {
					t.Fatal(e)
				}
			})
		}
	}
}
func TestTLSVerificationAndCancellation(t *testing.T) {
	cfg, _ := testServer(t, "tls", true)
	if c, e := Dial(context.Background(), cfg, []string{"itom-test"}); e == nil {
		c.Close()
		t.Fatal("accepted untrusted TLS")
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() { n, _ := ln.Accept(); accepted <- n }()
	cfg.Port = ln.Addr().(*net.TCPAddr).Port
	ctx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	start := time.Now()
	if c, e := Dial(ctx, cfg, []string{"itom-test"}); e == nil {
		c.Close()
		t.Fatal("hanging TLS accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation ignored")
	}
	if n := <-accepted; n != nil {
		n.Close()
	}
}
func TestMIMEBoundsAndUnknownAppend(t *testing.T) {
	if _, e := Encode(make([]byte, MaxBody+1)); e != ErrLimit {
		t.Fatal(e)
	}
	if _, e := Decode([]byte("bad")); e == nil {
		t.Fatal("bad MIME")
	}
	cfg, tc := testServer(t, "tls", true)
	ctx := context.Background()
	c, e := dial(ctx, cfg, []string{"itom-test"}, tc)
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	raw, _ := Encode([]byte("x"))
	if e = c.Append(ctx, "itom-test", raw); !errors.Is(e, ErrNotStarted) || errors.Is(e, ErrUnknown) {
		t.Fatal(e)
	}
	if e.Error() != ErrNotStarted.Error() || FailureReason(e) == "other" {
		t.Fatal("pre-publication refusal lost its safe socket cause", e)
	}
}

func TestRecent32SpansMultipleBoundedFetches(t *testing.T) {
	cfg, tc := testServer(t, "tls", true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-recent"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "itom-recent"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 40; i++ {
		raw, encErr := Encode([]byte("recent-" + strconv.Itoa(i)))
		if encErr != nil {
			t.Fatal(encErr)
		}
		if err = c.Append(ctx, "itom-recent", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, tail, err := c.Recent(ctx, "itom-recent", 32)
	if err != nil {
		t.Fatal(err)
	}
	if tail.After != 40 || len(rows) != 32 {
		t.Fatalf("tail=%+v rows=%d", tail, len(rows))
	}
	if rows[0].UID != 9 || rows[len(rows)-1].UID != 40 {
		t.Fatalf("unexpected recent range %d..%d", rows[0].UID, rows[len(rows)-1].UID)
	}
}

func TestPageTailLimitBoundsInteractiveFetchBatch(t *testing.T) {
	cfg, tc := testServer(t, "tls", true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"itom-tail-limit"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ensure(ctx, "itom-tail-limit"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		raw, err := Encode([]byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(ctx, "itom-tail-limit", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, cur, err := c.PageTailLimit(ctx, "itom-tail-limit", Cursor{}, true, 4)
	if err != nil || len(rows) != 4 || cur.After != 4 {
		t.Fatalf("first bounded tail rows=%d cur=%+v err=%v", len(rows), cur, err)
	}
	rows, next, err := c.PageTailLimit(ctx, "itom-tail-limit", cur, false, 4)
	if err != nil || len(rows) != 4 || next.After != 8 {
		t.Fatalf("second bounded tail rows=%d cur=%+v next=%+v err=%v", len(rows), cur, next, err)
	}
}
