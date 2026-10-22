//go:build itomtest

package tunnel

import (
	"testing"
	"time"
)

func validateTestCommitDelay(t *testing.T, server, client time.Duration) {}
func applyTestCommitDelay(l *lane, d time.Duration) {
	if d <= 0 {
		return
	}
	l.control.AddCommitDelayForTest(d)
	if l.side {
		l.data.AddCommitDelayForTest(d)
	}
}
