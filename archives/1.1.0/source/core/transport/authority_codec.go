package transport

import "itom.local/core/wire"

const maxAuthorityPayload = 4096
const maxAuthorityQueued = 64

// Authority records are stream-independent CONTROL work. Inner ledger class
// selects the ORIGINAL quota authority; the carrying mail still spends CONTROL.
func encodeAuthority(class LedgerClass, message wire.Control) ([]byte, error) {
	if class != DataLedger && class != ControlLedger {
		return nil, ErrProtocol
	}
	frame, err := wire.EncodeControl(0, message)
	if err != nil {
		return nil, err
	}
	raw, err := frame.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if len(raw)+2 > maxAuthorityPayload {
		return nil, ErrCapacity
	}
	return append([]byte{1, byte(class)}, raw...), nil
}
func decodeAuthority(payload []byte) (LedgerClass, wire.Control, error) {
	if len(payload) < 3 || len(payload) > maxAuthorityPayload || payload[0] != 1 {
		return 0, nil, ErrProtocol
	}
	class := LedgerClass(payload[1])
	if class != DataLedger && class != ControlLedger {
		return 0, nil, ErrProtocol
	}
	frame, err := wire.ParseFrame(payload[2:])
	if err != nil || frame.Stream != 0 {
		return 0, nil, ErrProtocol
	}
	message, err := wire.DecodeControl(frame)
	if err != nil {
		return 0, nil, ErrProtocol
	}
	switch message.(type) {
	case wire.StorageGrant, wire.UsageReport, wire.StorageRelease, wire.CleanupStatus, wire.Retirement:
		return class, message, nil
	default:
		return 0, nil, ErrProtocol
	}
}
func authorityKey(message wire.Control) wire.QuotaKey {
	switch m := message.(type) {
	case wire.StorageGrant:
		return m.Key
	case wire.UsageReport:
		return m.Key
	case wire.StorageRelease:
		return m.Key
	case wire.CleanupStatus:
		return m.Key
	case wire.Retirement:
		return m.Key
	}
	return wire.QuotaKey{}
}
func authorityProgress(payload []byte) bool {
	_, message, err := decodeAuthority(payload)
	if err != nil {
		return false
	}
	switch m := message.(type) {
	case wire.CleanupStatus:
		return true // updates an existing wallet; emits no new work
	case wire.Retirement:
		return m.Ack // existing final proof is mandatory in the wallet
	}
	return false
}
