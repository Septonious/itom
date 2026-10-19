//go:build itomtest

package journal

import (
	"itom.local/core/pool"
	"path/filepath"
	"testing"
	"time"
)

func TestPositiveCommitDelayRetainsDurabilityAndChargedExposure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s := openTest(t, path, testConfig(1))
	delay := 4 * time.Millisecond
	s.AddCommitDelayForTest(delay)
	begin := time.Now()
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 1})
	if time.Since(begin) < delay || s.db.NoSync || s.db.NoGrowSync {
		t.Fatal("commit delay replaced real durability")
	}
	s.Close()
	s = openTest(t, path, testConfig(1))
	heldTest(t, s, "a0", "12345678", g.Limit)
}
