package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestRC2IdleHedgeDistinctAndBounded(t *testing.T) {
	ctx := context.Background()
	l := &IMAPLink{ctx: ctx}
	for range 4 {
		l.accounts = append(l.accounts, &mailAccountState{link: l, ready: true, fast: make(chan mailWork, 64)})
	}
	run := func(seq uint64, want int) {
		t.Helper()
		wire := header(sealed, SessionID{1}, seq)
		if err := l.SendInteractive(ctx, wire); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, a := range l.accounts {
			if len(a.fast) > 1 {
				t.Fatal("copies routed to same account")
			}
			if len(a.fast) == 1 {
				w := <-a.fast
				if string(w.body) != string(wire) {
					t.Fatal("ciphertext changed")
				}
				count++
			}
		}
		if count != want {
			t.Fatalf("copies=%d want=%d", count, want)
		}
	}
	run(1, 3)
	l.lastInteractive.Store(time.Now().UnixNano())
	run(2, 1)
	l.lastInteractive.Store(time.Now().Add(-time.Second).UnixNano())
	l.lastBulk.Store(time.Now().UnixNano())
	run(3, 2)
	l.lastInteractive.Store(time.Now().Add(-time.Second).UnixNano())
	l.lastBulk.Store(time.Now().Add(-2 * time.Second).UnixNano())
	l.accounts[1].ready = false
	l.accounts[2].ready = false
	run(4, 2)
}
