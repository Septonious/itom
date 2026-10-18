package tunnel

import (
	"testing"
	"time"
)

func TestReaderWakeToFetchDelayOneShot(t *testing.T) {
	m := &laneMail{}
	if got := m.ReaderWakeToFetchDelay(); got != -1 {
		t.Fatalf("no request: %v", got)
	}
	m.SetReaderHot(time.Now().Add(time.Second))
	got := m.ReaderWakeToFetchDelay()
	if got < 0 || got > time.Second {
		t.Fatalf("bad wake latency %v", got)
	}
	if got := m.ReaderWakeToFetchDelay(); got != -1 {
		t.Fatalf("consumed twice: %v", got)
	}
}
func TestReaderWakeToFetchDelayRejectsStaleTime(t *testing.T) {
	m := &laneMail{}
	m.readerHotRequestAt.Store(time.Now().Add(-11 * time.Second).UnixNano())
	if got := m.ReaderWakeToFetchDelay(); got != -1 {
		t.Fatalf("stale: %v", got)
	}
	m.readerHotRequestAt.Store(time.Now().Add(time.Second).UnixNano())
	if got := m.ReaderWakeToFetchDelay(); got != -1 {
		t.Fatalf("future: %v", got)
	}
}
