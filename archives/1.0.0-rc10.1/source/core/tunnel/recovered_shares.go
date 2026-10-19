package tunnel

import (
	"itom.local/core/journal"
	"itom.local/core/pool"
)

// Preserve every historical charge, then divide only the genuinely remaining
// capacity between the two directions using complete lease units. A static
// local share let download take space returned by an old incoming lease before
// upload could refill it. Neither origin can borrow that refill cushion now.
func recoveredDirectionalShares(profile pool.Amount, u journal.OriginUsage) (local, remote pool.Amount, err error) {
	localBytes, remoteBytes := duplexDataShares(profile.Bytes)
	local = pool.Amount{Bytes: localBytes, Messages: profile.Messages / 4}
	remote = pool.Amount{Bytes: remoteBytes, Messages: profile.Messages - local.Messages}
	old := pool.Amount{Bytes: u.HistoricalLocal.Bytes + u.HistoricalRemote.Bytes, Messages: u.HistoricalLocal.Messages + u.HistoricalRemote.Messages}
	if old.Bytes > profile.Bytes || old.Messages > profile.Messages {
		return pool.Amount{}, pool.Amount{}, journal.ErrCorrupt
	}
	if old.Bytes == 0 && old.Messages == 0 {
		return local, remote, nil
	}
	available := pool.Amount{Bytes: profile.Bytes - old.Bytes, Messages: profile.Messages - old.Messages}
	remoteBytes = available.Bytes - available.Bytes/2
	// Two equally sized bulk leases plus fast permission must consist of whole
	// maximum BODYs. Give the otherwise unusable partial pair to local copies,
	// which also need a separate FETCH before their physical deletion proof.
	quantum := uint64(remoteBulkLeases * dataBatchMIMEHardCap)
	if remoteBytes >= fastLeaseBytes+quantum {
		remoteBytes = fastLeaseBytes + (remoteBytes-fastLeaseBytes)/quantum*quantum
	}
	// A quarter of the remaining ordinals can fill with small local BODYs while
	// most byte headroom sits idle in incoming reservations. Share both finite
	// resources between directions; normal debt-free profiles keep their shares.
	localNew := pool.Amount{Bytes: available.Bytes - remoteBytes, Messages: min(available.Messages, max(uint64(2), available.Messages/2))}
	local = pool.Amount{Bytes: u.HistoricalLocal.Bytes + localNew.Bytes, Messages: u.HistoricalLocal.Messages + localNew.Messages}
	remote = pool.Amount{Bytes: u.HistoricalRemote.Bytes + available.Bytes - localNew.Bytes, Messages: u.HistoricalRemote.Messages + available.Messages - localNew.Messages}
	return local, remote, nil
}
