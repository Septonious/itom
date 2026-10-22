package tunnel

import "time"

// Historical debt is not released or discounted here. Existing authenticated
// observation/deletion proofs and FIFO maintenance admission still govern IO.
// Scale only the next eligible remote-cleanup turn when the recovered inbound
// budget is smaller than a fresh profile. Fresh/unknown profiles keep RC2 cadence.
func (l *lane) remoteCleanupPeriod() time.Duration {
	const normal = runtimeRemoteCleanupInterval
	const floor = 1100 * time.Millisecond
	if !l.side || l.stripe == nil || l.data == nil || l.cipher == nil {
		return normal
	}
	u, err := l.data.UsageByOrigin(l.account.ID, l.user.ID, l.cipher.Context().Session)
	if err != nil || u.HistoricalLocal.Bytes == 0 && u.HistoricalRemote.Bytes == 0 && u.HistoricalLocal.Messages == 0 && u.HistoricalRemote.Messages == 0 {
		return normal
	}
	profile, err := l.data.Limit(l.account.ID, l.user.ID)
	if err != nil {
		return normal
	}
	_, remote, err := recoveredDirectionalShares(profile, u)
	if err != nil || remote.Bytes < u.HistoricalRemote.Bytes {
		return normal
	}
	available := remote.Bytes - u.HistoricalRemote.Bytes
	if available <= fastLeaseBytes {
		return floor
	}
	bulk := min(uint64(remoteBulkLeases*dataLeaseBytes), available-fastLeaseBytes)
	return max(floor, normal*time.Duration(bulk)/time.Duration(remoteBulkLeases*dataLeaseBytes))
}
