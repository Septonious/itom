package tunnel

import (
	imap "github.com/emersion/go-imap/v2"
	"sync"
	"testing"
	"time"
)

// One backend state across all sockets/users of an account. A clock is injected
// only into state tests; real socket/performance runs always use real time.
type providerServiceState struct {
	mu           sync.Mutex
	now          func() time.Time
	start        time.Time
	correlated   bool
	active       map[string]int
	limit        int
	peak         map[string]int
	denied       uint64
	accountDelay map[string]time.Duration
}

func (s *providerServiceState) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
func (s *providerServiceState) admit(account string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = map[string]int{}
		s.peak = map[string]int{}
	}
	if s.limit > 0 && s.active[account] >= s.limit {
		s.denied++
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeLimit, Text: "backend command limit"}
	}
	s.active[account]++
	s.peak[account] = max(s.peak[account], s.active[account])
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); s.active[account]--; s.mu.Unlock() }) }, nil
}
func (s *providerServiceState) extraDelay(account string) time.Duration {
	if s == nil {
		return 0
	}
	base := s.accountDelay[account]
	if !s.correlated {
		return base
	}
	// Every operation on the same backend gets the same queue spike. This is
	// independent of operation-name sample indices and connection scheduling.
	var hash uint64
	for _, c := range account {
		hash = hash*131 + uint64(c)
	}
	phase := s.clock().Sub(s.start) + time.Duration(hash%11)*time.Second
	if phase >= 0 && phase%(30*time.Second) < 2*time.Second {
		return base + 300*time.Millisecond
	}
	return base
}

func TestProviderStateVirtualClockAccountCapsVisibilityAndCorrelatedQueue(t *testing.T) {
	now := time.Unix(1000, 0)
	s := &providerServiceState{now: func() time.Time { return now }, start: now, correlated: true, limit: 2}
	a, _ := s.admit("account")
	b, _ := s.admit("account")
	if _, err := s.admit("account"); err == nil {
		t.Fatal("second user escaped shared cap")
	}
	other, err := s.admit("other")
	if err != nil {
		t.Fatal("foreign account blocked", err)
	}
	other()
	b()
	b()
	a()
	next, err := s.admit("account")
	if err != nil {
		t.Fatal("cap not released", err)
	}
	next()
	var hash uint64
	for _, c := range "account" {
		hash = hash*131 + uint64(c)
	}
	now = now.Add(30*time.Second - time.Duration(hash%11)*time.Second)
	if s.extraDelay("account") != 300*time.Millisecond || s.extraDelay("account") != 300*time.Millisecond {
		t.Fatal("account spike not correlated")
	}
	now = now.Add(2 * time.Second)
	if s.extraDelay("account") != 0 {
		t.Fatal("queue spike never recovers")
	}
	v := &providerVisibility{now: func() time.Time { return now }, until: map[visibilityKey]time.Time{{"account", "folder", 11}: now.Add(time.Second)}}
	hidden := v.visible("account", "folder", []imap.UID{10, 11, 12})
	if len(hidden) != 2 || hidden[0] != 10 || hidden[1] != 12 {
		t.Fatal("visibility allocation/order")
	}
	if got := v.visible("other", "folder", []imap.UID{11}); len(got) != 1 {
		t.Fatal("visibility leaked accounts")
	}
	now = now.Add(time.Second)
	if got := v.visible("account", "folder", []imap.UID{11}); len(got) != 1 {
		t.Fatal("exact expiry not visible")
	}
	network := newProviderTestNetwork(0)
	network.now = func() time.Time { return now }
	network.outageAccount("account", time.Second)
	if network.accountAvailable("account") || !network.accountAvailable("other") {
		t.Fatal("outage scope")
	}
	now = now.Add(time.Second)
	if !network.accountAvailable("account") {
		t.Fatal("outage failed to recover")
	}
}
