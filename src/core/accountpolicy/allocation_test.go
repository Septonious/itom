package accountpolicy

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func ids(prefix string, n int) []string {
	v := make([]string, n)
	for i := range v {
		v[i] = fmt.Sprintf("%s%02d", prefix, i)
	}
	return v
}

func TestBalancedCeilingAllocation(t *testing.T) {
	for _, tt := range []struct {
		n, u int
		want []int
	}{
		{0, 2, []int{0, 0}}, {8, 3, []int{8, 8, 8}}, {10, 1, []int{10}}, {15, 1, []int{8}},
		{15, 2, []int{8, 7}}, {15, 3, []int{5, 5, 5}}, {15, 4, []int{4, 4, 4, 3}},
		{16, 3, []int{6, 5, 5}}, {20, 3, []int{7, 7, 6}},
	} {
		accounts, users := ids("a", tt.n), ids("u", tt.u)
		got := Plan(accounts, users)
		covered := map[string]bool{}
		for i, user := range users {
			grant := got[user]
			if len(grant) != tt.want[i] {
				t.Fatalf("%d/%d %s: got %d want %d", tt.n, tt.u, user, len(grant), tt.want[i])
			}
			seen := map[string]bool{}
			for _, a := range grant {
				if seen[a] || !slices.Contains(accounts, a) {
					t.Fatalf("invalid grant %v", grant)
				}
				seen[a] = true
				covered[a] = true
			}
		}
		if tt.u > 1 && len(covered) != tt.n {
			t.Fatalf("abandoned accounts: %d/%d", len(covered), tt.n)
		}
		slices.Reverse(accounts)
		slices.Reverse(users)
		users = append(users, users[0], "")
		if !reflect.DeepEqual(got, Plan(accounts, users)) {
			t.Fatal("ordering or duplicate devices changed grants")
		}
	}
}

func TestAllocationCoverageForEverySupportedProfile(t *testing.T) {
	for n := 0; n <= 20; n++ {
		for u := 0; u <= 50; u++ {
			accounts, users := ids("a", n), ids("u", u)
			plan := Plan(accounts, users)
			if len(plan) != u {
				t.Fatal("identity count changed")
			}
			covered := map[string]bool{}
			minCount, maxCount := n, 0
			for _, grant := range plan {
				count := len(grant)
				minCount = min(minCount, count)
				maxCount = max(maxCount, count)
				if count > n || n > 10 && count < 3 {
					t.Fatal("invalid account count")
				}
				if n <= 10 && count != n {
					t.Fatal("small-profile allocation must be disabled")
				}
				seen := map[string]bool{}
				for _, a := range grant {
					if seen[a] {
						t.Fatal("duplicate account within grant")
					}
					seen[a] = true
					covered[a] = true
				}
			}
			if u > 1 && (len(covered) != n || maxCount-minCount > 1) {
				t.Fatalf("unfair plan %d/%d: %v", n, u, plan)
			}
			if u > 1 && n > 10 && n/u >= 3 {
				total := 0
				for _, grant := range plan {
					total += len(grant)
				}
				if total != n {
					t.Fatal("unnecessary overlap")
				}
			}
		}
	}
}
