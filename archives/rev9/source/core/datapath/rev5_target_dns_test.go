package datapath

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type rev5FailureResolver struct{ err error }

func (r rev5FailureResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return nil, r.err
}

// Failed target initialization must release its slot and leave the next TCP
// connection usable; diagnostics retain only fixed reasons, never DNS data.
func TestRev5TargetDNSFailureDoesNotPoisonNextConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"not_found", &net.DNSError{Name: "private-target.example", Server: "192.0.2.1", Err: "private error", IsNotFound: true}},
		{"temporary", &net.DNSError{IsTemporary: true}},
		{"timeout", &net.DNSError{IsTimeout: true}},
		{"canceled", context.Canceled},
		{"failed", errors.New("private error")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, l, _ := tcpFixture(t, true)
			x.cfg.Resolver = rev5FailureResolver{tc.err}
			failed := control(t, 1, wire.Connect{Host: "private-target.example", Port: 443})
			a, err := x.Request(0, failed)
			if err != nil {
				t.Fatal(err)
			}
			r := x.Dial(*a.Dial)
			if r.reason != wire.ReasonNetwork || r.diagnostic.Stage != "dns" || r.diagnostic.Reason != tc.name {
				t.Fatal("target failure classification", r.reason, r.diagnostic)
			}
			if text := fmt.Sprint(r.diagnostic); strings.Contains(text, "private") || strings.Contains(text, "192.0.2.1") {
				t.Fatal("DNS details leaked", text)
			}
			if _, c, err := x.Complete(time.Millisecond, r); err != nil || c != nil {
				t.Fatal("failed target completion", err)
			}
			next := control(t, 3, wire.Connect{Host: "127.0.0.1", Port: uint16(l.Addr().(*net.TCPAddr).Port)})
			a, err = x.Request(2*time.Millisecond, next)
			if err != nil {
				t.Fatal(err)
			}
			r = x.Dial(*a.Dial)
			if _, c, err := x.Complete(3*time.Millisecond, r); err != nil || c == nil {
				t.Fatal("next target lost after DNS failure", err)
			}
		})
	}
}
