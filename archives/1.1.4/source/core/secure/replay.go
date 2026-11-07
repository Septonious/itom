package secure

import "errors"

const replaySize uint64 = 65536

var ErrReplay = errors.New("REPLAY")

// The caller only commits a counter after authentication AND header binding.
type replayWindow struct {
	seen        [replaySize / 64]uint64
	highest     uint64
	initialized bool
}

func (w *replayWindow) check(n uint64) error {
	if !w.initialized || n > w.highest {
		return nil
	}
	if w.highest-n >= replaySize {
		return ErrReplay
	}
	slot := n % replaySize
	if w.seen[slot/64]&(uint64(1)<<(slot%64)) != 0 {
		return ErrReplay
	}
	return nil
}
func (w *replayWindow) commit(n uint64) {
	if !w.initialized {
		w.initialized = true
		w.highest = n
	} else if n > w.highest {
		delta := n - w.highest
		if delta >= replaySize {
			w.seen = [replaySize / 64]uint64{}
		} else {
			for i := uint64(1); i <= delta; i++ {
				slot := (w.highest + i) % replaySize
				w.seen[slot/64] &^= uint64(1) << (slot % 64)
			}
		}
		w.highest = n
	}
	slot := n % replaySize
	w.seen[slot/64] |= uint64(1) << (slot % 64)
}
