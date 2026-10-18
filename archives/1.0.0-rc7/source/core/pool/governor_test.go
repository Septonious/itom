package pool

import (
	"errors"
	"fmt"
	"testing"

	"itom.local/core/wire"
)

func accounts() []Account {
	var out []Account
	for i := 0; i < 10; i++ {
		out = append(out, Account{ID: fmt.Sprint(i), Provider: "shared-provider",
			DataLimit: Amount{2000, 20}, UserShares: map[string]Amount{"12345678": {1000, 10}, "87654321": {1000, 10}}})
	}
	return out
}
func checkTwoUsersShareAllTenAccounts(t *testing.T) {
	g, err := New(1, accounts(), 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		for j, user := range []string{"12345678", "87654321"} {
			if _, err = g.Issue(user, wire.ID{byte(j + 1)}, fmt.Sprint(i), Amount{1000, 10}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = g.Issue("12345678", wire.ID{1}, fmt.Sprint(i), Amount{1, 1}); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	}
	if len(g.Outstanding()) != 20 {
		t.Fatal("pool was partitioned")
	}
	if err = g.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkCloseReleasesUnusedButKeepsUnknownPhysicalBytes(t *testing.T) {
	g, err := New(1, accounts(), 128)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.Issue("12345678", wire.ID{1}, "0", Amount{1000, 10})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Usage(x.Token, Amount{600, 6}); err != nil {
		t.Fatal(err)
	}
	if err = g.Clean(x.Token, Amount{200, 2}); err != nil {
		t.Fatal(err)
	}
	if err = g.Close(x.Token, Amount{600, 6}); err != nil {
		t.Fatal(err)
	}
	if err = g.Close(x.Token, Amount{600, 6}); err != nil {
		t.Fatal(err)
	}
	if err = g.Usage(x.Token, Amount{300, 3}); err != nil {
		t.Fatal("late usage", err)
	}
	n, err := g.Held("0", "")
	if err != nil || n != (Amount{400, 4}) {
		t.Fatal(n, err)
	}
	if err = g.Clean(x.Token, Amount{600, 6}); err != nil {
		t.Fatal(err)
	}
	if err = g.Close(x.Token, Amount{600, 6}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err = g.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkWrongScopeAndCrossedCounters(t *testing.T) {
	g, err := New(1, accounts(), 128)
	if err != nil {
		t.Fatal(err)
	}
	x, err := g.Issue("12345678", wire.ID{1}, "0", Amount{1000, 10})
	if err != nil {
		t.Fatal(err)
	}
	bad := x.Token
	bad.User = "87654321"
	if err = g.Usage(bad, Amount{100, 1}); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if err = g.Usage(x.Token, Amount{600, 6}); err != nil {
		t.Fatal(err)
	}
	if err = g.Usage(x.Token, Amount{700, 5}); !errors.Is(err, ErrOrder) {
		t.Fatal(err)
	}
	if err = g.Clean(x.Token, Amount{700, 7}); !errors.Is(err, ErrOrder) {
		t.Fatal(err)
	}
	if err = g.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkProviderConsistencyAndConfigCopy(t *testing.T) {
	c := accounts()
	c[1].Provider = "another-provider"
	if _, err := New(1, c, 128); err == nil {
		t.Fatal("mixed provider accepted")
	}
	c = accounts()
	g, err := New(1, c, 128)
	if err != nil {
		t.Fatal(err)
	}
	c[0].UserShares["12345678"] = Amount{999999, 99999}
	if _, err = g.Issue("12345678", wire.ID{1}, "0", Amount{1001, 10}); !errors.Is(err, ErrLimit) {
		t.Fatal("external config mutated governor", err)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioGovernor(t *testing.T) {
	t.Log("check=checkTwoUsersShareAllTenAccounts")
	checkTwoUsersShareAllTenAccounts(t)
	t.Log("check=checkCloseReleasesUnusedButKeepsUnknownPhysicalBytes")
	checkCloseReleasesUnusedButKeepsUnknownPhysicalBytes(t)
	t.Log("check=checkWrongScopeAndCrossedCounters")
	checkWrongScopeAndCrossedCounters(t)
	t.Log("check=checkProviderConsistencyAndConfigCopy")
	checkProviderConsistencyAndConfigCopy(t)
}
