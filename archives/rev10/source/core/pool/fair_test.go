package pool

import "testing"

func TestFairnessAndBlockedUser(t *testing.T) {
	f, err := NewFair([]string{"12345678", "87654321"})
	if err != nil {
		t.Fatal(err)
	}
	costs := map[string]uint64{"12345678": 32 << 10, "87654321": 32 << 10}
	bytes := map[string]uint64{}
	for i := 0; i < 1024; i++ {
		u, ok := f.Next(costs)
		if !ok {
			t.Fatal("no eligible user")
		}
		bytes[u] += costs[u]
	}
	if bytes["12345678"] != bytes["87654321"] {
		t.Fatal(bytes)
	}
	costs["12345678"] = 0
	for i := 0; i < 128; i++ {
		u, ok := f.Next(costs)
		if !ok || u != "87654321" {
			t.Fatal("blocked user stalled pool", u)
		}
	}
	if f.deficit["12345678"] != 0 {
		t.Fatal("blocked user banked deficit")
	}
}
