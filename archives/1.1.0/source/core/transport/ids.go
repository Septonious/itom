package transport

const maxStreamID = uint64(1<<63 - 1)
const idHistory = uint64(65536)

// Separate origin/parity histories permit out-of-order OPEN arrivals while
// refusing retired IDs. Storage does not grow with the number of short TCP
// connections. An OPEN older than the bounded replay horizon fails closed.
type idSide struct {
	high        uint64
	initialized bool
	bits        [idHistory / 64]uint64
}
type idWindow struct{ side [2]idSide }

func (w *idWindow) available(id uint64) bool {
	q := &w.side[id&1]
	n := id / 2
	if !q.initialized || n > q.high {
		return true
	}
	if q.high-n >= idHistory {
		return false
	}
	slot := n % idHistory
	return q.bits[slot/64]&(uint64(1)<<(slot%64)) == 0
}
func (w *idWindow) commit(id uint64) {
	q := &w.side[id&1]
	n := id / 2
	if !q.initialized {
		q.initialized = true
		q.high = n
	} else if n > q.high {
		if n-q.high >= idHistory {
			q.bits = [idHistory / 64]uint64{}
		} else {
			for p := q.high + 1; p <= n; p++ {
				slot := p % idHistory
				q.bits[slot/64] &^= uint64(1) << (slot % 64)
			}
		}
		q.high = n
	}
	slot := n % idHistory
	q.bits[slot/64] |= uint64(1) << (slot % 64)
}
