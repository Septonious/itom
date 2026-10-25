package transport

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"

	"itom.local/core/wire"
)

const maxRosterAccounts = 4096 / 2 // bounded selected service, not configured accounts
const maxRosterBytes = 32 << 10
const rosterHeader = 100
const rosterEntry = 12

type RosterAccount struct{ Account, FastFolder, BulkFolder string }
type RosterPlan struct {
	Accounts                   []RosterAccount
	ClientBudget, ServerBudget RuntimeBudget
}
type RosterLane struct {
	Plane    Plane
	Executor *Executor
}
type RosterAccountState struct {
	RosterAccount
	FastUIDValidity, BulkUIDValidity uint32
}
type RosterManifest struct {
	ControlStore, DataStore    wire.ID
	ControlRun, DataRun        uint64
	Accounts                   []RosterAccountState
	ClientBudget, ServerBudget RuntimeBudget
}

func canonicalRosterPlan(plan RosterPlan) (RosterPlan, [32]byte, error) {
	var zero [32]byte
	n := len(plan.Accounts)
	if n == 0 || n > maxRosterAccounts {
		return RosterPlan{}, zero, ErrCapacity
	}
	for _, b := range []RuntimeBudget{plan.ClientBudget, plan.ServerBudget} {
		if b.Connections < 2*n || b.Connections > 4096 || b.PerAccount < 2 || b.PerAccount > 128 {
			return RosterPlan{}, zero, ErrProtocol
		}
	}
	plan.Accounts = append([]RosterAccount(nil), plan.Accounts...)
	sort.Slice(plan.Accounts, func(i, j int) bool { return plan.Accounts[i].Account < plan.Accounts[j].Account })
	raw := []byte("ITOM/session-v2/roster-plan/v1\x00")
	for _, b := range []RuntimeBudget{plan.ClientBudget, plan.ServerBudget} {
		raw = binary.BigEndian.AppendUint16(raw, uint16(b.Connections))
		raw = binary.BigEndian.AppendUint16(raw, uint16(b.PerAccount))
	}
	for n, a := range plan.Accounts {
		if n > 0 && a.Account == plan.Accounts[n-1].Account || a.FastFolder == a.BulkFolder {
			return RosterPlan{}, zero, ErrProtocol
		}
		shape := wire.QuotaKey{Store: wire.ID{1}, Grant: 1, Epoch: 1, Session: wire.ID{1}, User: "12345678", Account: a.Account}
		for _, folder := range []string{a.FastFolder, a.BulkFolder} {
			if _, err := wire.ServerEnvelopeSize(shape, wire.ServerControl, folder, 1); err != nil {
				return RosterPlan{}, zero, ErrProtocol
			}
		}
		for _, v := range []string{a.Account, a.FastFolder, a.BulkFolder} {
			raw = binary.BigEndian.AppendUint16(raw, uint16(len(v)))
			raw = append(raw, v...)
		}
	}
	return plan, sha256.Sum256(raw), nil
}
func encodeRoster(m RosterManifest, plan RosterPlan, hash [32]byte) ([]byte, error) {
	if m.ControlStore == (wire.ID{}) || m.DataStore == (wire.ID{}) || m.ControlStore == m.DataStore || m.ControlRun == 0 || m.DataRun == 0 || len(m.Accounts) != len(plan.Accounts) || m.ClientBudget != plan.ClientBudget || m.ServerBudget != plan.ServerBudget {
		return nil, ErrProtocol
	}
	size := rosterHeader + rosterEntry*len(plan.Accounts)
	if size > maxRosterBytes {
		return nil, ErrCapacity
	}
	raw := make([]byte, size)
	copy(raw, "IRS2")
	raw[4] = 1
	copy(raw[8:40], hash[:])
	copy(raw[40:56], m.ControlStore[:])
	binary.BigEndian.PutUint64(raw[56:64], m.ControlRun)
	copy(raw[64:80], m.DataStore[:])
	binary.BigEndian.PutUint64(raw[80:88], m.DataRun)
	for n, b := range []RuntimeBudget{m.ClientBudget, m.ServerBudget} {
		at := 88 + 4*n
		binary.BigEndian.PutUint16(raw[at:at+2], uint16(b.Connections))
		binary.BigEndian.PutUint16(raw[at+2:at+4], uint16(b.PerAccount))
	}
	binary.BigEndian.PutUint16(raw[96:98], uint16(len(m.Accounts)))
	for n, a := range m.Accounts {
		if a.RosterAccount != plan.Accounts[n] || a.FastUIDValidity == 0 || a.BulkUIDValidity == 0 {
			return nil, ErrProtocol
		}
		at := rosterHeader + n*rosterEntry
		binary.BigEndian.PutUint16(raw[at:at+2], uint16(n))
		binary.BigEndian.PutUint16(raw[at+2:at+4], 15) // actual writable keywords + UIDPLUS, both planes
		binary.BigEndian.PutUint32(raw[at+4:at+8], a.FastUIDValidity)
		binary.BigEndian.PutUint32(raw[at+8:at+12], a.BulkUIDValidity)
	}
	return raw, nil
}
func decodeRoster(raw []byte, plan RosterPlan, hash [32]byte) (RosterManifest, error) {
	var m RosterManifest
	if len(raw) < rosterHeader || len(raw) > maxRosterBytes || string(raw[:4]) != "IRS2" || raw[4] != 1 || raw[5] != 0 || raw[6] != 0 || raw[7] != 0 || raw[98] != 0 || raw[99] != 0 || string(raw[8:40]) != string(hash[:]) || int(binary.BigEndian.Uint16(raw[96:98])) != len(plan.Accounts) || len(raw) != rosterHeader+rosterEntry*len(plan.Accounts) {
		return m, ErrProtocol
	}
	copy(m.ControlStore[:], raw[40:56])
	m.ControlRun = binary.BigEndian.Uint64(raw[56:64])
	copy(m.DataStore[:], raw[64:80])
	m.DataRun = binary.BigEndian.Uint64(raw[80:88])
	m.ClientBudget = RuntimeBudget{int(binary.BigEndian.Uint16(raw[88:90])), int(binary.BigEndian.Uint16(raw[90:92]))}
	m.ServerBudget = RuntimeBudget{int(binary.BigEndian.Uint16(raw[92:94])), int(binary.BigEndian.Uint16(raw[94:96]))}
	for n, a := range plan.Accounts {
		at := rosterHeader + n*rosterEntry
		if binary.BigEndian.Uint16(raw[at:at+2]) != uint16(n) || binary.BigEndian.Uint16(raw[at+2:at+4]) != 15 {
			return RosterManifest{}, ErrProtocol
		}
		m.Accounts = append(m.Accounts, RosterAccountState{a, binary.BigEndian.Uint32(raw[at+4 : at+8]), binary.BigEndian.Uint32(raw[at+8 : at+12])})
	}
	if _, err := encodeRoster(m, plan, hash); err != nil {
		return RosterManifest{}, err
	}
	return m, nil
}
