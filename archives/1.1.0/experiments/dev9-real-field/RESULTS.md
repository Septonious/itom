# ITOM 1.1.0 DEV9 — experimental testing RC

DEV9 addresses the latest 28/30-line capture and the physical scheduling that delayed short requests. It sizes publication bodies to genuinely remaining quota, retaining every historical charge; protects an actual split DATA receive path; lends free command turns between the existing sockets; and makes both endpoints converge on the same short-response path. It adds no connection or quota allowance. Quota, wallet, journal, recovery proofs, Android package identity and persistent APK signer are preserved.

The matched original qualification passed **18/20 sustained rounds and 48/48 short phases**, versus unchanged RC2's 14/20 and 41/48. The exact latest quota shape reaches 30/30 directions and completes a 512 KiB single-account exchange within the original 5-second limit, followed by a 750 ms light-request check, on both fixed and variable-mobile paths. The separate cold HTTPS test passes 4/4 candidate cases, DEV8 3/4, RC2 2/4.

The following is a direct comparison against published DEV8 using identical 15-account fixtures, retained grants, captured socket roles, 500 Mbps client access and 20 ms server-to-IMAP propagation. These are simulator application measurements, not Rambler/Android speed predictions.

| Fixed 40 ms client path | DEV8 | DEV9 |
| --- | ---: | ---: |
| SINGLE idle / light mean |214 / 261 ms|200 / 225 ms|
| SINGLE download / upload mean |107.3 / 75.3 Mbps|106.3 / 100.3 Mbps|
| MULTI idle / light mean |242 / 237 ms|200 / 222 ms|
| MULTI download / upload mean |105.9 / 75.3 Mbps|106.2 / 98.3 Mbps|

On the variable 40–180 ms mobile path, SINGLE idle/light means fall 369/359→299/314 ms and MULTI 407/403→283/301 ms. The dedicated idle/light cases have no 750 ms misses for DEV9, versus 4 idle and 6 light misses for DEV8. However, the separate repeated TLS/ramp case **still fails**: it completes 22/48 and 33/48 light probes before failure, versus DEV8's 8/48 and 10/48. Those counts are completed probes, not Speedtest success rates. Variable-mobile capacity phases retain sub 100 Mbps results and half-second stalls; incomplete runs are not averaged away.

**This package is not fully qualified.** The original 1100 ms loaded-chat case fails in both normal and race-instrumented runs. The mobile post-TLS 750 ms light request also fails in the byte-headroom suite. Only 4/24 captured-role candidate phases meet both the original 100 Mbps mean-rate and zero-half-second-stall assertions, as does DEV8; the candidate completes 23/24 phases versus DEV8's 18/24. All failed deadlines, rates and stalls remain failures. First/last two-second rates are recorded separately. Full normal-suite invariant cases, the additional accounting/mailbox/recovery races, Android unit tests, compilation and lint are checked before publishing; this does not convert failed timing tests into passes.

Earlier 200+Mbps laboratory results used more sockets, generous free quota and long established transfers. They did not predict a two-socket phone's real speed. This RC shows a measured improvement over DEV8 under its captured constraints; it does **not** establish that 100 Mbps, low idle RTT or Speedtest reliability have been restored on the real provider.

Install **both DEV9 endpoints**. Update the APK in place using the existing persistent signer and unchanged `org.itom.client` identity. **Keep app data and wallets; keep the server's existing state directory.** Reinstalling or clearing data loses wallet history while the server correctly retains its old reservations.

Runtime source: `96a592f4a9278d069a248e38ddb645a914bde6aa`. Build workflow commit: `96f872809f03d18515794e350621508d53300fff`, with identical active source. The release tag points to the publication commit. Verify assets against `SHA256SUMS`; see `DEV9_BUILD.json`, `RESULTS.json` and the raw lab archive for exact provenance and every limitation.

Detailed observations: [RESULTS.json](RESULTS.json).
