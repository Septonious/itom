package mobile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"itom.local/core/socks"
	"itom.local/core/wire"
	"itom.local/mailstream"
)

func TestRC16NativeOpenReasons(t *testing.T) {
	c := new(Client)
	for _, err := range []error{socks.OpenError{Code: wire.ReasonPolicy}, mailstream.ErrCapacity, fmt.Errorf("wrapped: %w", mailstream.ErrClosed), context.Canceled, &net.DNSError{Err: "not found", Name: "private.example"}, syscall.ECONNREFUSED, context.DeadlineExceeded, errors.New("unclassified")} {
		c.traffic.recordOpenFailure(err)
	}
	s := c.trafficSnapshot()
	if s.OpenFailures != 8 || s.OpenPolicy != 1 || s.OpenCapacity != 1 || s.OpenClosed != 1 || s.OpenCanceled != 1 || s.OpenDNS != 1 || s.OpenRefused != 1 || s.OpenTimeouts != 1 || s.OpenOther != 1 {
		t.Fatalf("unexpected native counts: %+v", s)
	}
}

func TestRC2RemoteOpenDiagnosticsPreserveRelayReason(t *testing.T) {
	c := new(Client)
	for _, reason := range []mailstream.OpenFailureReason{mailstream.OpenFailureRelayTLS, mailstream.OpenFailureRelayCooldown, mailstream.OpenFailureDNS, mailstream.OpenFailureConnect} {
		c.traffic.recordOpenFailure(mailstream.RemoteOpenError{Reason: reason})
	}
	s := c.trafficSnapshot()
	if s.OpenFailures != 4 || s.OpenClosed != 0 || s.OpenDNS != 1 || s.OpenOther != 3 || s.RemoteOpenReasons["relay_tls"] != 1 || s.RemoteOpenReasons["relay_cooldown"] != 1 || s.RemoteOpenReasons["connect"] != 1 {
		t.Fatalf("unexpected %+v", s)
	}
}
