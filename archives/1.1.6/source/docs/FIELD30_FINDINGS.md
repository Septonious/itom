# FIELD30 — DATA capacity recovery, field acceptance pending

## Confirmed defects and correction

The shared server runtime permanently retired its optional DATA worker after a connection-cap refusal. Client activation also forgot a refused optional writer instead of repairing it. Both behaviors are reproduced by the new recovery scenario against the unchanged FIELD29 runtime: the server and client recovery cases each time out. This is a negative regression reproduction, not a final gate failure.

The same two server goroutines now suspend/recover optional capacity with paced authenticated redials. The unstarted request stays in the existing bounded queue, retaining exact BODY ownership. If even the mandatory writer is temporarily refused, it retains that same admitted request while redialing rather than reporting repeated unknown APPEND outcomes. Cancellation and other mandatory-writer dial errors remain errors. An already started APPEND is never retried here. The two-worker ceiling and all journal/refund rules remain unchanged.

Client activation retains refused writer positions and starts the existing repair mechanism. Retry delays grow from 1 to 30 seconds with 0.8–1.2 jitter. Control, receive and cleanup sockets remain intact during DATA repair. Capacity changes wake stripe scheduling and emit a `data_capacity` event. Connection diagnostics count authenticated sockets rather than empty repair positions. Close cancels and joins repair before returning.

## Application and regression checks

One recovery scenario covers optional server refusal with queued exact-once requests, mandatory server refusal before APPEND, and client activation refusal with real TLS/IMAP payload verification. The HTTP application scenario now has nominal and provider-cap-recovery profiles. The latter starts at nine sessions/account, observes actual refusal and reduced DATA capacity, lifts the cap to ten while bulk and fresh short requests continue, and requires both writers to return without replacing the logical session or carriers.

HTTP traffic uses actual HTTP/2/TLS upload/download, eight fresh HTTP/1.1 connections, four supervised accounts and 100 ms message-visibility delay. Default command delay is 15 ms; an explicit 80 ms profile was also checked. Each profile validates 4,718,592 bytes in each direction and payload integrity. These are local liveness/integrity scenarios, not a bandwidth/RTT qualification. None simulates losing more than half the accounts.

The existing 2 ms bounded TCP read coalescing was inspected. Small record classification alone is not evidence that mobile TCP fragments caused the field slowdown. No speculative scheduling change is included in this snapshot.

## Acceptance remains open

Required field goal: at least 100 Mbps separately in both directions with every loaded RTT strictly below 1000 ms. A local test pass cannot establish that goal on Android and a public IMAP provider. Synchronizing-literal performance and the original field TIMEOUT are not resolved by these capacity fixes. No claim is made that all Speedtest failures are fixed.

The consolidated test structure from FIELD29 is retained. Optional long performance/fault profiles remain explicit skips in the default gate, not passes. Final exact-source normal/race/build results are attached separately. A release still requires application/provider evidence and at least three paired sustained runs per relevant profile; more than 10% median degradation vetoes a candidate. Packet-loss and 200 Mbps work remain deferred. This is a development snapshot, not an accepted release.

## Final local gates

The stock server launcher completed normal and race gates: 401 PASS results, zero FAIL, three explicit optional skips and 18 packages in each mode. Structure: 267 Test scenarios, 4 fuzz targets, 96 resource/concurrent subcases and 34 seeds. Tunnel package elapsed 66.35 seconds normal and 109.302 seconds with race instrumentation. The launcher's unchanged timeouts are 120 and 300 seconds.

Native normal/race, IMAP fork normal/race, vet, module/pin checks and 64 Python/source checks passed. Linux amd64/arm64 binaries built. Windows tunnel cross-compilation passed; it is not Windows execution. The final 80 ms HTTP profile passed both nominal and temporary-cap scenarios. Archive self-check and exact source-manifest verification accompany the saved evidence. Android APK/device and live provider qualification were not performed.
