package transport

// A physical message is charged to exactly one authority. Plane describes its
// service path; LedgerClass describes its quota origin. Short DATA can use Fast
// but must still spend DATA allowance, never protocol reserve.
type LedgerClass uint8

const (
	AnyLedger LedgerClass = iota
	DataLedger
	ControlLedger
)

func (class LedgerClass) accepts(kind Kind) bool {
	return class == AnyLedger || class == DataLedger && kind == Data || class == ControlLedger && kind != Data
}
