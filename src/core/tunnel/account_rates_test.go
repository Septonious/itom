package tunnel

import (
	"testing"
	"time"
)

func TestAccountRatesShareWindowsAndReset(t *testing.T) {
	c := new(Client)
	now := time.Unix(100, 0)
	lines := []LineDiagnostic{{AccountID: "a01", Account: "user", UploadBytes: 100, DownloadBytes: 200}, {AccountID: "a02", Account: "user", UploadBytes: 300}}
	sample := func(at time.Time) { c.sampleAccountRates(lines, at) }
	sample(now)
	if lines[0].RateReady {
		t.Fatal("first observation has no rate window")
	}
	lines[0].UploadBytes += 1000
	lines[0].DownloadBytes += 2000
	lines[1].UploadBytes += 500
	sample(now.Add(time.Second))
	if !lines[0].RateReady || lines[0].UpBps != 8000 || lines[0].DownBps != 16000 || lines[1].UpBps != 4000 {
		t.Fatalf("rates: %+v", lines)
	}
	lines[0].Account = "renamed"
	lines[0].UploadBytes += 100
	sample(now.Add(time.Second + time.Millisecond))
	if lines[0].UpBps != 8000 {
		t.Fatal("second observer changed rate")
	}
	sample(now.Add(2 * time.Second))
	if lines[0].UpBps != 800 || lines[0].DownBps != 0 {
		t.Fatalf("idle/down window: %+v", lines[0])
	}
	sample(now.Add(3 * time.Second))
	if lines[0].UpBps != 0 || !lines[0].RateReady {
		t.Fatal("idle must display zero")
	}
	lines[0].UploadBytes = 1
	sample(now.Add(4 * time.Second))
	if lines[0].RateReady || lines[0].UpBps != 0 {
		t.Fatal("counter reset reused previous rate")
	}
	sample(now.Add(3 * time.Second))
	if lines[0].RateReady {
		t.Fatal("backwards clock retained rate")
	}
}
