# Temporary idle/light RTT maximum

User decision, 25 October 2026 (Moscow): use **300ms maximum** for idle and light
application exchanges for now. Continue reporting **250ms as the preferred
target**, to be restored as the acceptance maximum after real-world confirmation.
There is no automatic reversion based on a synthetic result alone.

Every measured exchange must meet the current maximum. Means and percentiles
cannot hide a spike; failed TLS/HTTP exchanges remain failures. Keep all raw
samples and report violations at both 300ms and 250ms. Historical result files
retain their original 250ms semantics and pass/fail outcomes. Re-scoring old
samples at the new maximum is a separate analysis, not a rewritten result.

Loaded RTT remains at most 2000ms. Sustained useful bandwidth remains at least
100Mbps in each direction, including SINGLE/MULTI/duplex and the original full
measurement, ramp, tail, rolling-window and stall checks. Unexpected TLS or
Speedtest failures and load-induced reconnects remain unacceptable. Quota,
journal, recovery, memory, socket and package/signing requirements are unchanged.

This changes only the idle/light acceptance maximum. It does not change DNS's
250ms race timeout, the 250ms network-profile phase or quiet audit timer, nor any
OPEN/TLS/HTTP deadline or IMAP command lifetime. The preceding checkpoint's
336ms maximum still exceeds the temporary limit; this decision alone does not
qualify that checkpoint or authorize a deployable release.
