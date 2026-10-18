package mailbox

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapserver"
)

type rev9Dialer struct {
	calls   int
	address string
}

func (d *rev9Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.calls++
	d.address = address
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

type rev9LoginSession struct {
	imapserver.Session
	logins *atomic.Int32
}

func (s *rev9LoginSession) Login(username, password string) error {
	s.logins.Add(1)
	return s.Session.Login(username, password)
}

func TestRev9PinnedIPPreservesTLSAndPersistentLogin(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			var logins atomic.Int32
			cfg, tc := testServerWithSession(t, mode, true, func(s imapserver.Session) imapserver.Session { return &rev9LoginSession{Session: s, logins: &logins} })
			cfg.Host = "example.com"
			cfg.DialIP = "127.0.0.1"
			tc = tc.Clone()
			tc.ServerName = cfg.Host
			dialer := &rev9Dialer{}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := dialNetwork(ctx, cfg, []string{"pinned"}, tc, false, dialer)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			id := c.conn.id
			if err = c.Ensure(ctx, "pinned"); err != nil {
				t.Fatal(err)
			}
			raw, _ := Encode([]byte("one authenticated persistent connection"))
			for i := 0; i < 3; i++ {
				if err = c.Append(ctx, "pinned", raw); err != nil {
					t.Fatal(err)
				}
				if _, err = c.Ping(ctx); err != nil {
					t.Fatal(err)
				}
			}
			rows, _, err := c.Page(ctx, "pinned", Cursor{})
			if err != nil || len(rows) != 3 {
				t.Fatal(len(rows), err)
			}
			// Exercise the actual idle auxiliary keepalive without waiting twenty seconds.
			c.conn.lastIO.Store(time.Now().Add(-25 * time.Second).UnixNano())
			c.keepAliveIfIdle(time.Now())
			if dialer.calls != 1 || logins.Load() != 1 || c.conn.id != id || !c.Ready() {
				t.Fatal("healthy work/keepalive reconnected", dialer.calls, logins.Load())
			}
			host, _, _ := net.SplitHostPort(dialer.address)
			if host != cfg.DialIP {
				t.Fatal("pin was not used", host)
			}
		})
	}
}
func TestRev9PinNeverDisablesTLSOrFallsBackToDNS(t *testing.T) {
	cfg, tc := testServer(t, "tls", true)
	cfg.Host = "wrong-name.invalid"
	cfg.DialIP = "127.0.0.1"
	tc = tc.Clone()
	tc.ServerName = cfg.Host
	d := &rev9Dialer{}
	_, err := dialNetwork(context.Background(), cfg, []string{"pinned"}, tc, false, d)
	if err == nil || err.Error() != "IMAP_TLS_FAILED" || d.calls != 1 {
		t.Fatal("TLS verification or explicit endpoint bypassed", err, d.calls)
	}
	for _, v := range []string{"imap.example.org", "127.0.0.1:993", "[::1]", "fe80::1%eth0", "0.0.0.0", "::", "239.0.0.1", "ff02::1", " 81.19.77.168"} {
		cfg.DialIP = v
		if err = cfg.Validate(); !errors.Is(err, ErrScope) {
			t.Fatal("invalid pin accepted", v, err)
		}
	}
	for _, v := range []string{"", "81.19.77.168", "2001:db8::1", "::1"} {
		if err = ValidateDialIP(v); err != nil {
			t.Fatal(v, err)
		}
	}
}
