package tunnel

import (
	"context"
	"itom.local/core/wire"
	"testing"
)

func collectionLane() *lane {
	return &lane{ctx: context.Background(), urgentQueue: make(chan queuedControl, 64), receiptQueue: make(chan queuedControl, 4), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 8)}
}

func TestControlExpiredTimerDrainsReadyStartupBurstWithoutLoss(t *testing.T) {
	for repeat := 0; repeat < 100; repeat++ {
		l := collectionLane()
		for id := uint64(2); id <= 24; id++ {
			l.urgentQueue <- queuedControl{Frame: wire.Frame{Type: wire.Open, Stream: id}}
		}
		batch, live := l.collectControlBatch(queuedControl{Frame: wire.Frame{Type: wire.Open, Stream: 1}}, 0)
		if !live || len(batch) != controlBatch || len(l.urgentQueue) != 8 {
			t.Fatal("ready burst fragmented", len(batch), len(l.urgentQueue))
		}
		seen := map[uint64]bool{}
		for _, f := range batch {
			seen[f.Stream] = true
		}
		for len(l.urgentQueue) > 0 {
			f := <-l.urgentQueue
			if seen[f.Stream] {
				t.Fatal("duplicate")
			}
			seen[f.Stream] = true
		}
		if len(seen) != 24 {
			t.Fatal("lost control")
		}
	}
}
func TestControlReadyBurstCarriesReceiptAndMonotonicCredit(t *testing.T) {
	l := collectionLane()
	for id := uint64(2); id <= 24; id++ {
		l.urgentQueue <- queuedControl{Frame: wire.Frame{Type: wire.Open, Stream: id}}
	}
	l.receiptQueue <- queuedControl{Frame: wire.Frame{Type: wire.MailReceipt}}
	batch, live := l.collectControlBatch(queuedControl{Frame: wire.Frame{Type: wire.Open, Stream: 1}}, 0)
	if !live || len(batch) != 16 || batch[4].Type != wire.MailReceipt {
		t.Fatal("receipt starved", batch)
	}
	batch = []queuedControl{{Frame: wire.Frame{Type: wire.Window, Stream: 1, Offset: 100}}}
	batch = l.appendControlBatch(batch, queuedControl{Frame: wire.Frame{Type: wire.Window, Stream: 1, Offset: 90}}, 1)
	batch = l.appendControlBatch(batch, queuedControl{Frame: wire.Frame{Type: wire.Window, Stream: 1, Offset: 120}}, 1)
	if len(batch) != 1 || batch[0].Offset != 120 {
		t.Fatal("credit regressed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l.ctx = ctx
	if _, live = l.collectControlBatch(batch[0], 0); live {
		t.Fatal("cancellation ignored")
	}
}
