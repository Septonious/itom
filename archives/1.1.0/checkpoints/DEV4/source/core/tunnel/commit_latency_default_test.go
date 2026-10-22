//go:build !itomtest

package tunnel

import (
	"testing"
	"time"
)

func validateTestCommitDelay(t *testing.T, server, client time.Duration) {
	if server > 0 || client > 0 {
		t.Fatal("positive commit delay requires -tags=itomtest")
	}
}
func applyTestCommitDelay(l *lane, d time.Duration) {}
