package tunnel

import (
	"testing"
	"time"
)

func TestRC3HotResponseBackoffBound(t *testing.T) {
	idle := 0
	var total time.Duration
	for i := 0; i < 100; i++ {
		d := readerResponsePollDelay(1, true, false, 0, &idle, true)
		if d > 20*time.Millisecond {
			t.Fatal("hot response backoff exceeds 20ms", d)
		}
		if d < 10*time.Millisecond {
			t.Fatal("hot response polling can spin", d)
		}
		total += d
	}
	if total < time.Second {
		t.Fatal("unbounded command pressure")
	}
	if d := readerPollDelay(0, false, false, 0, &idle); d < 250*time.Millisecond {
		t.Fatal("hot pacing leaked into quiet period", d)
	}
	if d := readerPollDelay(1, true, true, 1, &idle); d != 0 || idle != 0 {
		t.Fatal("visible backlog was delayed")
	}
}
