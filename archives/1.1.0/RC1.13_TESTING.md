# ITOM 1.1.0 RC1.13 — startup scheduling and socket lifetime

The RC1.12 exports now identify a concrete failure chain. Historical EXPUNGE
commands do not complete within their original 30-second watchdog. Four account
workers retain the login slots throughout inventory and cleanup, so ten accounts
take about 97–100 seconds to reach rendezvous. The phone's original startup limit
is 90 seconds. Early-selected server sockets also wait without keepalive while
later account groups run, and are already closed at rendezvous.

RC1.13 limits concurrent LOGIN/TLS/SELECT work to the original configured login
parallelism, but lets already-selected accounts complete their bounded inventory
independently. Completed service sockets receive the existing side-effect-free
NOOP keepalive until every account is ready. Keepalive ownership is stopped and
joined before rendezvous takes over. These changes do not add connections,
increase quotas, extend deadlines, skip authenticated inventory or waive cleanup
proofs. A stalled cleanup still retains its original physical debt.

The regression combines ten accounts, retained authenticated local mail carrying
valid per-body receipts, historical EXPUNGE stalls, and a 20-second provider idle
lease. It checks real startup, DATA and HTTPS with persistent journal paths.
The published RC1.12 server fails this reproduction at 90 seconds. The packaged
RC1.13 server completes startup in 32.8 seconds and carries authenticated DATA,
including the variant with an early-selected account waiting on nine stalled
accounts. Source HTTPS, native DNS, SOCKS and TUN checks also pass.

Complete functional checks pass 320 core groups and 14 native groups under the
race detector, plus 101 Android unit tests and all eight packaged-artifact groups,
including 20 persistent restart cycles. Source light-load results are 64/64 within
300 ms (maximum 273 ms); after application silence, 3/3 pass (maximum 224 ms).
The upper-latency scenario still fails the full goal: 11/16 within 300 ms, maximum
343 ms. Old and corrected results accompany this release. Local TLS fixtures do
not certify Rambler or an Android device. Sustained 100 Mbps and the full 300 ms
RTT goal remain unqualified.

Update both endpoints in place. Run the downloaded server executable and choose
11, then 9. Its fresh trace must show itom-1-1-0-rc1-13. Install the APK over the
existing app; preserve profiles, app data, wallets and server journals.
