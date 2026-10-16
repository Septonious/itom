package tunnel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itom.local/core/wire"
)

type parallelQuietTestMail struct {
	isolatedRoleTestMail
	parallel int
}

func (m *parallelQuietTestMail) DataParallelism() int { return m.parallel }

// A reader reserved for short DATA may already have one APPEND in progress.
// Its second physical slot still serves that reader; independent control I/O
// must not route the next request into a bulk BODY queue instead.
func TestQuietReaderUsesRemainingSlotDuringIndependentControlAppend(t *testing.T) {
	g := newStripeGroup(wire.ID{6}, 1)
	var lanes []*lane
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.mail = &parallelQuietTestMail{parallel: 2}
		l.lastProgress.Store(time.Now().UnixNano())
		l.physicalSlots = make(chan struct{}, 2)
		if i <= 2 {
			l.physicalSlots <- struct{}{}
			l.controlWriting.Store(1)
		}
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
		lanes = append(lanes, l)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	selected, key, err := g.takeRemoteGrantForClass(ctx, lanes[0], true)
	if err != nil {
		t.Fatal(err)
	}
	selected.fastGrants <- key
	if selected != lanes[0] && selected != lanes[1] {
		t.Fatal("free reserved slot lost to bulk BODY reader", selected.account.ID)
	}
	lease, ok := g.tryAcquireFastLane(selected)
	if !ok || len(selected.physicalSlots) != 2 {
		t.Fatal("remaining slot was not usable")
	}
	lease.release()
	if len(selected.physicalSlots) != 1 {
		t.Fatal("fast reservation changed existing physical debt")
	}
	// Filling both reserves must still allow fallback, without overbooking.
	for _, l := range lanes[:2] {
		l.physicalSlots <- struct{}{}
	}
	selected, key, err = g.takeRemoteGrantForClass(ctx, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	selected.fastGrants <- key
	if selected == lanes[0] || selected == lanes[1] {
		t.Fatal("full reader reserve bypassed physical slot bound")
	}
	// Socket availability can reduce the effective limit below channel cap.
	for _, l := range lanes[:2] {
		<-l.physicalSlots
		l.mail.(*parallelQuietTestMail).parallel = 1
	}
	selected, _, err = g.takeRemoteGrantForClass(ctx, nil, true)
	if err != nil || selected == lanes[0] || selected == lanes[1] {
		t.Fatal("reader reserve ignored effective socket capacity", err)
	}
}
