package tunnel

import "time"

type accountRate struct {
	at             time.Time
	up, down       uint64
	upBps, downBps uint64
	ready          bool
}

// Client.mu owns the samples. Diagnostics, trace and UI calls share one rate
// window; a second observer must not turn a one-second delta into a tiny spike.
func (c *Client) sampleAccountRates(lines []LineDiagnostic, now time.Time) {
	if c.rates == nil {
		c.rates = make(map[string]accountRate)
	}
	for i := range lines {
		line := &lines[i]
		id := line.AccountID
		if id == "" {
			id = line.Account
		}
		previous, ok := c.rates[id]
		if !ok || now.Before(previous.at) || line.UploadBytes < previous.up || line.DownloadBytes < previous.down {
			previous = accountRate{at: now, up: line.UploadBytes, down: line.DownloadBytes}
		} else if elapsed := now.Sub(previous.at); elapsed >= 500*time.Millisecond {
			previous.upBps = uint64(float64(line.UploadBytes-previous.up) * 8 / elapsed.Seconds())
			previous.downBps = uint64(float64(line.DownloadBytes-previous.down) * 8 / elapsed.Seconds())
			previous.at, previous.up, previous.down = now, line.UploadBytes, line.DownloadBytes
			previous.ready = true
		}
		c.rates[id] = previous
		line.UpBps, line.DownBps, line.RateReady = previous.upBps, previous.downBps, previous.ready
	}
}
