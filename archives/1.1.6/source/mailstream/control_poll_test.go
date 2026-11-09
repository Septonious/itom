package mailstream

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestRadioControlPollBackoffAndTrafficReset(t *testing.T) {
	for _, tc := range []struct{ idle, want time.Duration }{
		{0, time.Millisecond},
		{1500*time.Millisecond, time.Millisecond},
		{2*time.Second, 3*time.Millisecond},
		{9*time.Second, 3*time.Millisecond},
		{10*time.Second, 8*time.Millisecond},
		{29*time.Second, 8*time.Millisecond},
		{30*time.Second, 16*time.Millisecond},
		{time.Hour, 16*time.Millisecond},
	} {
		if got:=controlPollInterval(tc.idle);got!=tc.want {
			t.Fatalf("idle=%v: got %v, want %v",tc.idle,got,tc.want)
		}
	}
	if got:=(&mailPipe{}).controlPollDelay(time.Now());got!=time.Millisecond {
		t.Fatalf("isolated readers must keep the original polling delay, got %v",got)
	}
	var activity atomic.Int64
	now:=time.Now()
	activity.Store(now.Add(-time.Minute).UnixNano())
	p:=&mailPipe{pollTraffic:&activity}
	if got:=p.controlPollDelay(now);got!=16*time.Millisecond {
		t.Fatalf("quiet pipe did not back off: %v",got)
	}
	activity.Store(now.UnixNano())
	if got:=p.controlPollDelay(now);got!=time.Millisecond {
		t.Fatalf("new activity did not restore 1ms: %v",got)
	}
	activity.Store(now.Add(time.Minute).UnixNano())
	if got:=p.controlPollDelay(now);got!=time.Millisecond {
		t.Fatalf("future activity must fail open to fast polling: %v",got)
	}
}

func TestRadioControlPollCommandReductionBound(t *testing.T) {
	// The 7ms FETCH service is a model, not a measured radio bitrate.
	service:=7*time.Millisecond
	active:=time.Second/(service+controlPollInterval(0))
	quiet:=time.Second/(service+controlPollInterval(time.Minute))
	if active<100 || quiet>50 || quiet>=active/2 {
		t.Fatalf("expected fewer empty FETCHs: active=%d/s quiet=%d/s",active,quiet)
	}
}
