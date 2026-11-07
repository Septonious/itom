package mailstream

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A failed candidate must not consume a round-robin turn. Otherwise the first
// healthy account receives every earlier account's share of receipt traffic.
func TestFeedbackBurstsRotateAcrossAvailableAccounts(t *testing.T) {
	for _, ready := range [][]int{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {5, 6, 7, 8, 9}, {1, 3, 4, 7, 9}, {9}} {
		t.Run(fmt.Sprint(ready), func(t *testing.T) {
			l := &IMAPLink{ctx: context.Background(), client: true, changed: make(chan struct{})}
			id := SessionID{1}
			available := map[int]bool{}
			for _, n := range ready {
				available[n] = true
			}
			for n := 0; n < 10; n++ {
				l.accounts = append(l.accounts, &mailAccountState{link: l, config: MailAccount{ID: fmt.Sprintf("a%02d", n+1)}, id: id, ready: available[n], fast: make(chan mailWork, 128), bulk: make(chan mailWork, 16)})
			}
			for n := 0; n < 80; n++ {
				l.feedbackAt = time.Time{}
				if err := l.SendFeedback(context.Background(), header(sealed, id, uint64(n+1))); err != nil {
					t.Fatal(err)
				}
			}
			for n, a := range l.accounts {
				want := 0
				if available[n] {
					want = 80 / len(ready)
				}
				if len(a.fast) != want {
					t.Fatalf("account%d got%d receipt publications; expected%d across ready accounts%v", n, len(a.fast), want, ready)
				}
			}
		})
	}
}
