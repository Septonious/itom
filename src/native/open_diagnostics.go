package mobile

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	"itom.local/core/socks"
	"itom.local/core/wire"
	"itom.local/mailstream"
)

// Categorize native application-open errors without retaining addresses,
// hostnames, credentials or arbitrary error text. A peer RESET remains
// Closed/unknown rather than being misreported as a proven policy failure.
func (d *trafficDiagnostics) recordOpenFailure(err error) {
	d.openFailures.Add(1)
	var reason mailstream.RemoteOpenError
	if errors.As(err, &reason) && reason.Reason >= mailstream.OpenFailurePolicy && reason.Reason <= mailstream.OpenFailureRelayCooldown {
		d.remoteOpenReasons[reason.Reason].Add(1)
		switch reason.Reason {
		case mailstream.OpenFailurePolicy:
			d.openPolicy.Add(1)
		case mailstream.OpenFailureDNS:
			d.openDNS.Add(1)
		case mailstream.OpenFailureTimeout:
			d.openTimeouts.Add(1)
		case mailstream.OpenFailureCanceled:
			d.openCanceled.Add(1)
		default:
			d.openOther.Add(1)
		}
		return
	}
	var remote socks.OpenError
	var netErr net.Error
	var dns *net.DNSError
	if errors.As(err, &remote) {
		switch remote.Code {
		case wire.ReasonPolicy:
			d.openPolicy.Add(1)
		case wire.ReasonCapacity:
			d.openCapacity.Add(1)
		case wire.ReasonTimeout:
			d.openTimeouts.Add(1)
		case wire.ReasonClosed:
			d.openClosed.Add(1)
		case wire.ReasonRefused:
			d.openRefused.Add(1)
		default:
			d.openOther.Add(1)
		}
		return
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		d.openTimeouts.Add(1)
	case errors.Is(err, context.Canceled):
		d.openCanceled.Add(1)
	case errors.Is(err, mailstream.ErrCapacity), errors.Is(err, mailstream.ErrBusy):
		d.openCapacity.Add(1)
	case errors.Is(err, mailstream.ErrClosed), errors.Is(err, net.ErrClosed):
		d.openClosed.Add(1)
	case errors.As(err, &dns):
		d.openDNS.Add(1)
	case errors.Is(err, syscall.ECONNREFUSED):
		d.openRefused.Add(1)
	default:
		d.openOther.Add(1)
	}
}
