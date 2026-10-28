# ITOM 1.1.0 RC1.9 — startup and command ownership audit

This experimental build addresses two independent blockers in the paired RC1.8
exports: APPEND overlapping UID EXPUNGE before a server EOF, and exhausted
CONTROL capacity on the fixed first bootstrap account. It also fixes recovery
metadata starving fresh grants and pending retries changing their original
amount. See RC1.9_AUDIT.md for findings, scope and reproduction details.

EXPUNGE now drains all earlier tags and holds the writer until its own tag
completes. Later APPEND/FETCH/STORE/NOOP wait. Ordinary APPEND/FETCH concurrency
remains. Cancellation cannot abandon an admitted destructive command; Close
joins its lease. Exact authenticated BODY/UID scope and independent absence
proof are still required for cleanup.

Bootstrap chooses existing available CONTROL headroom. The client checks all
existing FAST lanes, accepting only a complete authenticated invitation bound
to its fresh original wallet session. CONTROL and DATA may use different
healthy accounts; all accounts need not have fresh grants for startup. The real
first DATA echo is still required before VPN READY. Existing wallet files,
profiles, limits and unknown historical debt are preserved. No timeout or
connection budget was increased, and no automatic reconnect was added.

Update BOTH endpoints in place. Run the newly downloaded server executable,
choose option 11 to install that executable, then option 9 to restart. Running
the old installed `itom` and choosing 11 reinstalls that old executable. Fresh
trace events must say `itom-1-1-0-rc1-9`. Install the APK over the existing app;
retain app data, server profiles and journals.

Confirm a measured RTT and open a small HTTPS website before speedtests. If
startup fails, export both full diagnostics while the failure is present. The
client now records bounded permission-wait snapshots before native startup;
quota and wallet errors also have specific fixed reason codes.

Functional reproductions do not establish sustained bandwidth or live Rambler
compatibility. No Android device or provider connection is available here.
Sustained 100 Mbps and maximum 300 ms light-load RTT remain unqualified. The
release evidence records functional, timing and build results, including any
failed qualification gates.

DNS is unchanged: race 31.131.251.83 and 185.22.234.229 over UDP/TCP 53, shared
250 ms timeout, optional IMAP fallback latched until the user restarts the VPN.

## Validation

Both hosted full functional runs pass 293 core and 14 native race-test groups,
plus vet. The local full race run has four failures, retained in the evidence.
A sequential non-race comparison of those four scenarios passes in both RC1.8
and RC1.9 (47.938 and 32.184 seconds for the cohorts). This does not establish
sustained throughput or field reliability.

The separate strict 300 ms RTT qualification still fails: warm 62/64
(maximum 325.610 ms), upper-range 7/16 (maximum 392.075 ms); after silence 3/3
(maximum 235.281 ms). This is a testing release for the audited connectivity
fixes. Complete passing and failing synthetic logs accompany it.
