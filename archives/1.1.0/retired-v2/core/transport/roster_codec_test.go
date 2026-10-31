package transport

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"itom.local/core/secure"
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
		m.Accounts = append(m.Accounts, RosterAccountState{RosterAccount: a, FastUIDValidity: 91, BulkUIDValidity: 92})
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
		"wrong_plan":          func(b []byte) { b[8] ^= 1 },
		"same_issuer":         func(b []byte) { copy(b[64:80], b[40:56]) },
		"zero_run":            func(b []byte) { clear(b[80:88]) },
		"larger_budget":       func(b []byte) { binary.BigEndian.PutUint16(b[88:90], 21) },
		"missing_account":     func(b []byte) { binary.BigEndian.PutUint16(b[96:98], 9) },
		"missing_keywords":    func(b []byte) { binary.BigEndian.PutUint16(b[102:104], 14) },
		"zero_incarnation":    func(b []byte) { clear(b[104:108]) },
		"reordered_entry":     func(b []byte) { binary.BigEndian.PutUint16(b[100:102], 1) },
		"reserved_bit":        func(b []byte) { b[99] = 1 },
		"zero_account_issuer": func(b []byte) { clear(b[rosterHeader+12 : rosterHeader+60]) },
		"cross_ledger_alias": func(b []byte) {
			copy(b[rosterHeader+rosterEntry+12:rosterHeader+rosterEntry+28], b[rosterHeader+36:rosterHeader+52])
		},
		"same_issuer_conflicting_run": func(b []byte) {
			binary.BigEndian.PutUint64(b[rosterHeader+rosterEntry+28:rosterHeader+rosterEntry+36], 999)
		},
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
		m.Accounts = append(m.Accounts, RosterAccountState{RosterAccount: a, FastUIDValidity: 1, BulkUIDValidity: 2})
	}
	raw, err := encodeRoster(m, plan, hash)
	if err != nil || len(raw) > maxRosterManifestBytes {
		t.Fatal(len(raw), err)
	}
	if _, err := decodeRoster(raw, plan, hash); err != nil {
		t.Fatal(err)
	}
	d := &RosterDriver{plan: plan, planHash: hash, input: &BootstrapHandoff{origin: ServerOrigin{Account: "a0", Store: m.ControlStore, Run: m.ControlRun}}}
	for range plan.Accounts {
		d.lanes = append(d.lanes, RosterLane{Plane: Fast, Executor: &Executor{validity: 1}}, RosterLane{Plane: Bulk, Executor: &Executor{validity: 2}})
	}
	digest := sha256.Sum256(raw)
	if candidate, _, complete, err := d.validateOfferChunk(rosterChunk(raw, maxRosterChunk, digest)); err != nil || complete || candidate != nil {
		t.Fatal("missing first chunk became a complete offer", complete, err)
	}
	first := rosterChunk(raw, 0, digest)
	if _, _, _, err := d.validateOfferChunk(first[:len(first)-1]); err == nil {
		t.Fatal("short non-final chunk changes publication count")
	}
	chunks := 0
	d.offerParts = map[int][]byte{}
	for offset := ((len(raw) - 1) / maxRosterChunk) * maxRosterChunk; offset >= 0; offset -= maxRosterChunk {
		chunk := rosterChunk(raw, offset, digest)
		if len(chunk) > maxRosterBytes {
			t.Fatal("roster packet bound increased")
		}
		if _, err := secure.SessionPacketSize(9 + len(chunk)); err != nil {
			t.Fatal("original secure packet bound exceeded", err)
		}
		if d.offerTotal > 0 {
			bad := append([]byte(nil), chunk...)
			bad[8] ^= 1
			if _, _, _, err := d.validateOfferChunk(bad); err == nil {
				t.Fatal("mixed offer hash accepted")
			}
		}
		candidate, got, complete, err := d.validateOfferChunk(chunk)
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			if len(candidate) != len(raw) || len(got.Accounts) != maxRosterAccounts {
				t.Fatal("partial manifest became complete")
			}
		} else {
			d.offerParts[offset] = append([]byte(nil), chunk[rosterChunkHeader:]...)
			d.offerTotal = len(raw)
			d.offerDigest = digest
		}
		chunks++
	}
	if chunks != 4 || 8+chunks+2 > maxBootstrapUIDs {
		t.Fatal("selected capacity exceeded original startup UID knowledge bound", chunks)
	}
}
