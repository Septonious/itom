package tunnel

import "sync/atomic"

// Counters persist through carrier replacement for the duration of this client.
type accountView struct{ upload, download atomic.Uint64 }
