package mailstream

import (
	"context"
	"testing"
)

func TestInheritedDataDebtRequestsRecoveryWithBulkHeadroom(t *testing.T) {
	for _, tc := range []struct {
		name             string
		spent, recovered int
		want             bool
	}{
		{"small crash rounding", 1 << 20, 1 << 20, false},
		{"substantial old DATA", 20 << 20, 20 << 20, true},
		{"same current DATA", 20 << 20, 0, false},
		{"quarter inherited", prototypePhysicalBudget / 4, prototypePhysicalBudget / 4, true},
		{"late small inherited", prototypePhysicalBudget * 3 / 4, 1 << 20, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := SessionID{1}
			l := &IMAPLink{ctx: context.Background(), client: true, attached: id}
			a := &mailAccountState{link: l, config: MailAccount{ID: "account"}, spent: tc.spent, recovered: tc.recovered, control: &mailPipe{validity: 1}}
			a.maintenance.epoch = id
			l.accounts = []*mailAccountState{a}
			if got := a.storagePressureLocked(); got != tc.want {
				t.Fatalf("inherited DATA recovery priority=%v, want%v with%d bytes still free", got, tc.want, prototypePhysicalBudget-tc.spent)
			}
			frames := l.maintenanceFrames(id)
			advertised := false
			for _, f := range frames {
				if f.kind == frameStoragePressure {
					advertised = f.data[len(f.data)-1] == 1
				}
			}
			if advertised != tc.want {
				t.Fatal("recovery priority not advertised to the server")
			}
			if a.spent != tc.spent || a.recovered != tc.recovered {
				t.Fatal("requesting recovery refunded unproved storage")
			}
		})
	}
}
