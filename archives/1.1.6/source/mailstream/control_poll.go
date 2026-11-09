package mailstream

import "time"

// The standing UID FETCH loop is necessary to avoid the extra IDLE/DONE RTT
// for latency-sensitive control. Polling at 1ms forever, however, wastes
// metered radio traffic across all configured mail accounts while VPN is quiet.
// The activity clock is shared by accepted outgoing packets and received
// control packets. Active users retain the original 1ms gap; quiet readers
// gradually back off to a hard 16ms maximum, without adding connections or
// changing the mailbox protocol.
func controlPollInterval(quiet time.Duration) time.Duration {
	switch {
	case quiet < 2*time.Second:
		return time.Millisecond
	case quiet < 10*time.Second:
		return 3 * time.Millisecond
	case quiet < 30*time.Second:
		return 8 * time.Millisecond
	default:
		return 16 * time.Millisecond
	}
}

func (p *mailPipe) controlPollDelay(now time.Time) time.Duration {
	if p.pollTraffic == nil {
		return time.Millisecond
	}
	last := p.pollTraffic.Load()
	if last <= 0 || now.UnixNano() < last {
		return time.Millisecond
	}
	return controlPollInterval(time.Duration(now.UnixNano() - last))
}
