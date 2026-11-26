package tunnel

import (
	"fmt"
	mailstream "itom.local/mailstream"
	"slices"
	"testing"
)

func TestRC4FieldGrantAvoidsInheritedDebt(t *testing.T) {
	// Reproduce the measured RC3 server ledger at startup, in decimal MB.
	retained := []int{66730000, 66720000, 66700000, 66760000, 66770000, 2170000, 9660000, 42480000, 8530000, 37010000, 4100000, 16730000, 16130000, 12700000, 10150000}
	a := &sessionAdmission{limit: 3, owners: map[string]int{}}
	var lines []mailstream.MailAccountSnapshot
	for i, debt := range retained {
		id := fmt.Sprintf("a%02d", i+1)
		a.accounts = append(a.accounts, id)
		lines = append(lines, mailstream.MailAccountSnapshot{Account: id, Ready: true, RetainedBytes: debt, RecoveredBytes: debt, BulkHeadroomBytes: max(0, (64<<20)-(512<<10)-debt), StoragePressure: debt >= 16<<20})
	}
	preferred := preferredGrantAccounts(lines)
	r1, err := a.acquirePreferred("one", preferred)
	if err != nil {
		t.Fatal(err)
	}
	defer r1()
	g1 := a.grant("one")
	want := []string{"a06", "a07", "a09", "a11", "a12", "a13", "a14", "a15"}
	sorted := slices.Clone(g1)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Fatalf("grant %v, expected %v", sorted, want)
	}
	r2, err := a.acquirePreferred("two", preferred)
	if err != nil {
		t.Fatal(err)
	}
	defer r2()
	if !slices.Equal(g1, a.grant("one")) || len(a.grant("two")) != 7 {
		t.Fatal("grant size or stable first allocation changed")
	}
	for _, id := range a.grant("two") {
		if slices.Contains(g1, id) {
			t.Fatal("unnecessary overlap")
		}
	}
}
