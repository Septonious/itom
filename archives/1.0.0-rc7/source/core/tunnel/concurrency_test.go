package tunnel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

func checkReceiptBeforeAppendCompletion(t *testing.T) {
	l := &lane{mailReceipts: map[uint64]bool{11: true}, obsolete: map[journal.AttemptRef]bool{}}
	ref := journal.AttemptRef{ID: 7}
	l.registerOutgoing(outgoing{ref: ref, counters: []uint64{11}, records: 1})
	if !l.obsolete[ref] || len(l.out[0].counters) != 0 || l.out[0].records != 1 {
		t.Fatal("early authenticated receipt lost or physical credit removed")
	}
}
func checkLatePresenceResultPreservesNewReceipts(t *testing.T) {
	old := mailbox.Message{Validity: 1, UID: 4}
	newMessage := mailbox.Message{Validity: 1, UID: 5}
	l := &lane{known: map[uint64]mailbox.Message{1: old, 2: newMessage}}
	if err := l.finishCleanup(cleanupResult{checked: map[uint64]mailbox.Message{1: old}, present: map[uint32]bool{}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.known[1]; ok {
		t.Fatal("old locator retained")
	}
	if l.known[2].UID != 5 {
		t.Fatal("new uninspected receipt lost")
	}
}
func checkNoFallbackForTLSOrCancellation(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("IMAP_TLS_FAILED"), io.ErrUnexpectedEOF, nil} {
		if connectionLimitFallback(err) {
			t.Fatal("invalid fallback", err)
		}
	}
	if !connectionLimitFallback(errors.New("IMAP_AUTH_OR_GREETING_FAILED")) {
		t.Fatal("connection-limited provider cannot use existing TLS socket")
	}
}
func checkUrgentBurstNeverDemotesToNormalQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{ctx: ctx, controlQueue: make(chan queuedControl, 4), urgentQueue: make(chan queuedControl, 1)}
	f, _ := wire.EncodeControl(0, wire.Echo{Token: 1})
	if err := l.SendControl(ctx, f); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := l.SendControl(ctx, f); err == nil || err.Error() != "CONTROL_QUEUE_LIMIT" {
		t.Fatalf("second urgent send err=%v", err)
	}
	if len(l.controlQueue) != 0 {
		t.Fatal("urgent control was silently demoted to normal queue")
	}
	if elapsed := time.Since(started); elapsed < 200*time.Millisecond || elapsed > 600*time.Millisecond {
		t.Fatalf("urgent backpressure=%v, want bounded ~250ms wait", elapsed)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioConcurrency(t *testing.T) {
	t.Log("check=checkReceiptBeforeAppendCompletion")
	checkReceiptBeforeAppendCompletion(t)
	t.Log("check=checkLatePresenceResultPreservesNewReceipts")
	checkLatePresenceResultPreservesNewReceipts(t)
	t.Log("check=checkNoFallbackForTLSOrCancellation")
	checkNoFallbackForTLSOrCancellation(t)
	t.Log("check=checkUrgentBurstNeverDemotesToNormalQueue")
	checkUrgentBurstNeverDemotesToNormalQueue(t)
}
