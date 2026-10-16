package mailbox

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Reproduce failures below the protocol: an actual DNS NXDOMAIN / stalled DNS,
// a refused local TCP socket and a dial deadline. No provider is contacted.
func TestConnectionFailuresIdentifyStageWithoutProviderText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg, tc := testServer(t, "tls", false)
	check := func(t *testing.T, err error, stage, reason, code string) {
		t.Helper()
		d := ConnectionDetail(fmt.Errorf("connect wrapper: %w", err))
		if err == nil || d.Stage != stage || d.Reason != reason || err.Error() != code {
			t.Fatalf("detail=%+v err=%v want=%s/%s/%s", d, err, stage, reason, code)
		}
		if strings.Contains(fmt.Sprintf("%+v", err), "secret") {
			t.Fatal("error exposed provider identity or password")
		}
	}
	t.Run("dns", func(t *testing.T) {
		for _, stalled := range []bool{false, true} {
			conn, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			go func() {
				b := make([]byte, 4096)
				for {
					n, peer, err := conn.ReadFrom(b)
					if err != nil {
						return
					}
					if stalled || n < 12 {
						continue
					}
					// Echo the DNS question with an authoritative NXDOMAIN response.
					binary.BigEndian.PutUint16(b[2:4], 0x8183)
					clear(b[6:12])
					_, _ = conn.WriteTo(b[:n], peer)
				}
			}()
			c := cfg
			c.Host = "private-secret.invalid"
			dialer := &net.Dialer{Timeout: 150 * time.Millisecond, Resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "udp", conn.LocalAddr().String())
			}}}
			_, err = DialWithNetwork(ctx, c, []string{"itom-check"}, dialer)
			reason := "not_found"
			if stalled {
				reason = "timeout"
			}
			check(t, err, "dns", reason, "IMAP_CONNECT_FAILED")
		}
	})
	t.Run("tcp_refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		c := cfg
		c.Port = port
		_, err = Dial(ctx, c, []string{"itom-check"})
		check(t, err, "tcp", "refused", "IMAP_CONNECT_FAILED")
	})
	t.Run("tcp_deadline", func(t *testing.T) {
		dialer := &net.Dialer{Timeout: 50 * time.Millisecond, ControlContext: func(ctx context.Context, _, _ string, _ syscall.RawConn) error {
			<-ctx.Done()
			return ctx.Err()
		}}
		_, err := DialWithNetwork(ctx, cfg, []string{"itom-check"}, dialer)
		check(t, err, "tcp", "timeout", "IMAP_CONNECT_FAILED")
	})
	t.Run("tls", func(t *testing.T) {
		_, err := Dial(ctx, cfg, []string{"itom-check"})
		check(t, err, "tls", "certificate_authority", "IMAP_TLS_FAILED")
	})
	t.Run("login", func(t *testing.T) {
		c := cfg
		c.Password = "wrong-private-secret"
		_, err := dial(ctx, c, []string{"itom-check"}, tc)
		check(t, err, "login", "rejected", "IMAP_AUTH_OR_GREETING_FAILED")
	})
	t.Run("unknown_provider_error", func(t *testing.T) {
		err := connectionFailed("IMAP_AUTH_OR_GREETING_FAILED", "greeting", errors.New("secret.example password=private"))
		check(t, err, "greeting", "other", "IMAP_AUTH_OR_GREETING_FAILED")
		if errors.Unwrap(err) != nil {
			t.Fatal("raw provider cause retained")
		}
	})
	t.Run("successful_dial", func(t *testing.T) {
		client, err := dial(ctx, cfg, []string{"itom-check"}, tc)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.Ping(ctx); err != nil {
			t.Fatal(err)
		}
		if d := ConnectionDetail(nil); d != (ConnectionFailure{}) {
			t.Fatal(d)
		}
	})
	t.Run("windows_socket_codes", func(t *testing.T) {
		for code, want := range map[syscall.Errno]string{10013: "permission", 10051: "unreachable", 10065: "unreachable", 10054: "reset", 10060: "timeout", 10061: "refused"} {
			err := &net.OpError{Op: "dial", Net: "tcp", Err: code}
			if got := windowsSocketReason(err); got != want {
				t.Fatalf("code=%d reason=%s want=%s", code, got, want)
			}
		}
		if windowsSocketReason(errors.New("private secret")) != "" {
			t.Fatal("unknown error reclassified")
		}
	})
}

func TestRev6LoginResponseCodesAreBoundedAndNeverEchoProviderText(t *testing.T) {
	for _, code := range []imap.ResponseCode{imap.ResponseCodeAuthenticationFailed, imap.ResponseCodeUnavailable, imap.ResponseCodeTooMany, imap.ResponseCode("SECRET_PROVIDER_CODE")} {
		err := connectionFailed("IMAP_AUTH_OR_GREETING_FAILED", "login", &imap.Error{Type: imap.StatusResponseTypeNo, Code: code, Text: "private-password private-user"})
		d := ConnectionDetail(err)
		if d.Stage != "login" || d.Reason != "rejected" {
			t.Fatal(d)
		}
		if code == "SECRET_PROVIDER_CODE" {
			if d.ResponseCode != "" {
				t.Fatal("unknown provider code exposed")
			}
		} else if d.ResponseCode != string(code) {
			t.Fatal("known code lost", d)
		}
		if strings.Contains(fmt.Sprintf("%+v %+v", err, d), "private") {
			t.Fatal("provider text exposed")
		}
	}
}
