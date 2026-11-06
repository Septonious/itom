package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestInteractiveBurstAvoidsFullPoolDuplicationButIsolatedRequestHedges(t *testing.T) {
	ctx := context.Background()
	l := &IMAPLink{ctx: ctx}
	for range 2 {
		l.accounts = append(l.accounts, &mailAccountState{link: l, ready: true, fast: make(chan mailWork, 64)})
	}
	count := func() int { return len(l.accounts[0].fast) + len(l.accounts[1].fast) }
	for i := range 16 {
		if err := l.SendInteractive(ctx, header(sealed, SessionID{1}, uint64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	if count() >= 32 {
		t.Fatal("simultaneous small requests still mirrored every publication")
	}
	time.Sleep(25 * time.Millisecond)
	before := count()
	if err := l.SendInteractive(ctx, header(sealed, SessionID{1}, 17)); err != nil {
		t.Fatal(err)
	}
	if count()-before != 2 {
		t.Fatal("isolated small request lost its two-account hedge")
	}
}
