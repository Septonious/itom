package mailstream

import "time"

// A previously timed-out provider metadata mutation may need more than the
// original 8s budget even after the retry is reduced to 32 messages/4 MB.
// Allow only quarantined/degraded collections 18 seconds; normal collectors
// stay at 8s. Timeout never refunds unverified physical mailbox bytes.
const collectionRetryTimeout = 18 * time.Second

func collectionAttemptTimeout(previousFailure bool) time.Duration {
	if previousFailure {
		return collectionRetryTimeout
	}
	return collectionTimeout
}
