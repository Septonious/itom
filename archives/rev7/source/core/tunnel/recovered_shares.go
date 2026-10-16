package tunnel

import (
	"itom.local/core/journal"
	"itom.local/core/pool"
)

// Preserve every historical charge, then divide only the genuinely remaining
// capacity evenly between the two directions. A static
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
	localNew := pool.Amount{Bytes: available.Bytes / 2, Messages: min(available.Messages, max(uint64(2), available.Messages/4))}
	local = pool.Amount{Bytes: u.HistoricalLocal.Bytes + localNew.Bytes, Messages: u.HistoricalLocal.Messages + localNew.Messages}
	remote = pool.Amount{Bytes: u.HistoricalRemote.Bytes + available.Bytes - localNew.Bytes, Messages: u.HistoricalRemote.Messages + available.Messages - localNew.Messages}
	return local, remote, nil
}
