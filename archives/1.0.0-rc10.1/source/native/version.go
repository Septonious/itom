package mobile

import "itom.local/core/tunnel"

// Version is bound into every AAR and APK so stale native code is detectable.
func Version() string { return tunnel.TransportRevision }
