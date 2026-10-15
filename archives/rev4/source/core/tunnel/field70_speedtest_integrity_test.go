package tunnel

import (
	"strings"
	"testing"
	"time"
)

// Qualification must cover the SAME last ten consecutive seconds for every
// user. Duplicated, delayed, or missing samples cannot masquerade as 40/40.
func TestField70SpeedtestTailRejectsGapsDuplicatesAndStaleUsers(t *testing.T) {
	const mb = int64(6 << 20)
	original := make([]productionBandwidthWindow, 0, 60)
	for i := 1; i <= 30; i++ {
		for user := 0; user < 2; user++ {
			original = append(original, productionBandwidthWindow{User: user, Index: i, Duration: time.Second, Upload: mb, Download: mb})
		}
	}
	check := func(label string, windows []productionBandwidthWindow, wantOK bool) {
		t.Helper()
		e := speedtestWindowQualification(windows, 30*time.Second, 2, 40)
		if (e == nil) != wantOK {
			t.Fatalf("%s: unexpected qualification error=%v", label, e)
		}
	}
	check("complete 30-second duplex", original, true)
	// All 40/40 bytes are present except for a single measurement interval.
	gaps := make([]productionBandwidthWindow, 0, len(original))
	for _, w := range original {
		if w.Index != 25 {
			gaps = append(gaps, w)
		}
	}
	check("missing second 25 in both directions", gaps, false)
	// New data for one user ends a second earlier. Never combine tails that
	// were measured at different times to claim simultaneous goodput.
	stale := make([]productionBandwidthWindow, 0, len(original))
	for _, w := range original {
		if !(w.User == 1 && w.Index == 30) {
			stale = append(stale, w)
		}
	}
	check("one user missing final second", stale, false)
	duplicates := append([]productionBandwidthWindow(nil), original...)
	for i := range duplicates {
		if duplicates[i].User == 1 && duplicates[i].Index == 27 {
			duplicates[i].Index = 26
			break
		}
	}
	check("duplicate interval conceals missing second", duplicates, false)
	extended := append([]productionBandwidthWindow(nil), original...)
	for i := range extended {
		if extended[i].Index >= 21 {
			extended[i].Duration = 2 * time.Second
		}
	}
	check("inflated sample duration", extended, false)
	noDownload := append([]productionBandwidthWindow(nil), original...)
	for i := range noDownload {
		if noDownload[i].User == 1 && noDownload[i].Index >= 21 {
			noDownload[i].Download = 0
		}
	}
	check("one user's collapsed download", noDownload, false)
	badUser := append([]productionBandwidthWindow(nil), original...)
	badUser = append(badUser, productionBandwidthWindow{User: 3, Index: 30, Duration: time.Second, Upload: mb, Download: mb})
	if err := speedtestWindowQualification(badUser, 30*time.Second, 2, 40); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid user accepted: %v", err)
	}
}
