package transport

import (
	"encoding/binary"
	"fmt"
	"testing"

	"itom.local/core/wire"
)

func TestRosterWireRejectsChangedBudgetsIncarnationsOrderAndPlan(t *testing.T) {
	plan := RosterPlan{ClientBudget: RuntimeBudget{20, 2}, ServerBudget: RuntimeBudget{30, 3}}
	for n := range 10 {
		plan.Accounts = append(plan.Accounts, RosterAccount{fmt.Sprintf("account%d", n), "fast", "bulk"})
	}
	plan, hash, err := canonicalRosterPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	m := RosterManifest{ControlStore: wire.ID{1}, ControlRun: 17, DataStore: wire.ID{2}, DataRun: 23, ClientBudget: plan.ClientBudget, ServerBudget: plan.ServerBudget}
	for _, a := range plan.Accounts {
		m.Accounts = append(m.Accounts, RosterAccountState{a, 91, 92})
	}
	raw, err := encodeRoster(m, plan, hash)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRoster(raw, plan, hash)
	if err != nil || len(got.Accounts) != 10 || got.DataStore != m.DataStore || got.DataRun != 23 {
		t.Fatal(got, err)
	}
	for name, change := range map[string]func([]byte){
		"wrong_plan":       func(b []byte) { b[8] ^= 1 },
		"same_issuer":      func(b []byte) { copy(b[64:80], b[40:56]) },
		"zero_run":         func(b []byte) { clear(b[80:88]) },
		"larger_budget":    func(b []byte) { binary.BigEndian.PutUint16(b[88:90], 21) },
		"missing_account":  func(b []byte) { binary.BigEndian.PutUint16(b[96:98], 9) },
		"missing_keywords": func(b []byte) { binary.BigEndian.PutUint16(b[102:104], 14) },
		"zero_incarnation": func(b []byte) { clear(b[104:108]) },
		"reordered_entry":  func(b []byte) { binary.BigEndian.PutUint16(b[100:102], 1) },
		"reserved_bit":     func(b []byte) { b[99] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := append([]byte(nil), raw...)
			change(bad)
			if _, err := decodeRoster(bad, plan, hash); err == nil {
				t.Fatal("invalid wire roster accepted")
			}
		})
	}
	for _, bad := range [][]byte{raw[:len(raw)-1], append(append([]byte(nil), raw...), 0)} {
		if _, err := decodeRoster(bad, plan, hash); err == nil {
			t.Fatal("noncanonical size accepted")
		}
	}
}

func TestRosterSelectedCapacityFitsOriginalSecurePacketBound(t *testing.T) {
	// The limit bounds selected sockets, not the number of configured accounts.
	plan := RosterPlan{ClientBudget: RuntimeBudget{4096, 2}, ServerBudget: RuntimeBudget{4096, 3}}
	for n := range maxRosterAccounts {
		plan.Accounts = append(plan.Accounts, RosterAccount{fmt.Sprintf("a%d", n), "fast", "bulk"})
	}
	plan, hash, err := canonicalRosterPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	m := RosterManifest{ControlStore: wire.ID{1}, ControlRun: 1, DataStore: wire.ID{2}, DataRun: 1, ClientBudget: plan.ClientBudget, ServerBudget: plan.ServerBudget}
	for _, a := range plan.Accounts {
		m.Accounts = append(m.Accounts, RosterAccountState{a, 1, 2})
	}
	raw, err := encodeRoster(m, plan, hash)
	if err != nil || len(raw) > maxRosterBytes {
		t.Fatal(len(raw), err)
	}
	if _, err := decodeRoster(raw, plan, hash); err != nil {
		t.Fatal(err)
	}
}
