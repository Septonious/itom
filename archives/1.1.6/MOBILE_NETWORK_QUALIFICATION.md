# ITOM weak-mobile-first qualification — development policy

**Primary operating environment:** metered and unstable cellular links, often below 20 Mbps, with jitter, asymmetric capacity, packet loss and radio handovers. 500 Mbps home tests are a regression guard, **not** the primary design target.

**Released reference:** `v1.1.6-rc1`, source `a134e4a05f8aa7cb83cf455129bd633c5c90c96e`. User field reference on a 500 Mbps home link: approximately **402 Mbps down, 357 Mbps up, 169 ms idle, 194 ms download-loaded, 189 ms upload-loaded**. Synthetic 20/5 Mbps test gave **14.36 Mbps down and 3.60 Mbps up**; do not mistake this for a cellular measurement. Original release and user data remain unchanged.

## Mandatory mobile acceptance matrix

Each proposed runtime change must be compared against *the released 1.1.6 RC1*, not just the older 1.1.5 baseline. Use multiple independent A/B trials with order alternation and exact source identification.

| Network | Down / up link | RTT and impairment | Core checks |
|---|---|---|---|
| Low mobile | 20 / 5 Mbps | 30–150 ms varying RTT | verified application goodput, MIME/IMAP bytes per useful MB, idle and loaded RTT p50/p95 |
| Poor mobile | 8 / 2 Mbps | 80–300 ms RTT and bursts | stalls, zero-data periods, retransmissions, per-account pressure |
| Very poor mobile | 2 / 0.5 Mbps | 150–500 ms RTT, brief outages | connection establishment, bounded memory/storage, verified eventual progress |
| Handover | 5–20 / 1–5 Mbps | 3–30 s interruption and IP change | finite reconnect, strict TLS identity, no stale session escape or data loss |
| Lossy | 2–20 / 0.5–5 Mbps | 0.5%, 2%, 5% packet loss | completion, duplicate handling, RTO, bounded retransmissions |
| High-capacity guard | ~500 / 500 Mbps | 3 ms network floor and 15–30 ms IMAP model | no major 350–400 Mbps regression; loaded RTT tails stable |

The fixture now supports integer-Mbps and exact kbps shaping for 2/0.5 Mbps, a generated 150–500 ms very-poor mobile latency profile, and temporary rate dips. It **does not yet implement real transport loss or IP handovers**; those require separate fault-injection infrastructure, not fabricated successful CI claims.

**Prioritize:** transfer completion, session survival/reconnect, battery wakeups, IMAP commands and total wire bytes, correct mailbox retention/cleanup, loaded RTT p95, then median RTT and peak throughput. A speed gain is invalid if it makes the VPN unusable after a weak-radio interruption or increases retained storage without audited cleanup.

## Current candidate techniques

1. **RC2 decision: no quiet-state polling backoff.** The initial candidate's 3/8/16 ms intervals were rejected because the first incoming packet after inactivity could incur up to **15 ms additional scheduling latency**. Keep the exact released RC1 **1 ms** delay even when idle. Empty FETCH overhead remains a known limitation; an event-driven replacement is not permitted until it proves non-increasing first-packet latency across all required profiles.
2. Keep mandatory 7bit ASCII85. The new valid MIME sender address reduces each APPEND by 16 bytes with no wire-version change. RC2 also grows wrap lines from 480 to 960 octets (below the RFC 998-octet limit), shaving about 0.2% of bulk ciphertext wire bytes without adding batching delays. Evaluate future BINARY only if **actual authenticated IMAP capabilities** show support; BINARY requires provider interoperability, integrity, storage and rollback qualification.
3. Distinguish TLS/LOGIN **transport** EOF/timeouts from permanent TLS verification or credential rejection. Existing exponential startup backoff must handle temporary failures without weakening certificate checks.
4. Account-specific cleanup retries can use a finite **18-second** mutation deadline *only after* prior failure, when their page remains restricted to 32 messages / 4 MB. Never refund mailbox debt without authoritative physical deletion proof.
5. Keep the released primary round-robin placement and ordinary feedback batching: both immediate-prompt-feedback and RTT-aware hedging produced worse loaded tails in paired synthetic trials.

## Known incomplete work

- Established-session IMAP TCP failures can still require a new VPN session. Android currently records network changes and can terminate a failed session, but automatic controlled restart after an interrupted active session needs its **own** design, tests and policy checks.
- The user's server diagnostics showed nearly **787 MB** of retained mailbox data, 11/15 accounts in storage pressure and multiple quarantined cleanup operations; verify finite clearance on live providers before any stable promotion.
- Instrument radio uplink and downlink *including repeated IMAP polling and TLS framing*, not just encoded payload bytes. Android current diagnostic snapshots can report capability booleans and poll interval but do not yet count physical TLS bytes.
- GitHub Actions runner allocation was failing before executing any steps at this development head. Do not label the candidate validated or publish an APK from it until runner jobs, full race tests, native+Android checks and paired mobile tests complete successfully.

## RC2 strict no-regression release rule (not yet qualified)

- Compare the proposed branch with immutable **released** `v1.1.6-rc1` source, not with an untested previous commit.
- Each completed paired run must have no *positive* measured RTT delta in **idle, loaded and after-load minimum/median/p95/p99/maximum**. A single higher metric **blocks the release**; do not average away a regression.
- Both upload and download goodput must remain at least 75% of RC1 in each trial; also compare the best throughput across trials to protect maximum throughput. No fallback to a 200 Mbps absolute floor as a substitute for the 25% relative rule.
- Five alternating-order synthetic A/B trials per network profile are the minimum screen (500 Mbps home, 3 ms home-icmp floor, 160/90 mobile, 20/5 mobile, 8/2 poor mobile, and 2/0.5 very poor mobile at 150–500 ms). Runner jitter can make strict gates fail; that is a reason to gather more evidence, not to silently relax the constraint.
- The fixture cannot prove a literal zero-RTT-change guarantee on every real network, or model physical cellular loss/handover. Separate packet-loss/handover and actual phone/provider trials remain mandatory before a general release.
- The Go toolchain and Android integration must pass, and matched server/APK artifacts must be built, signed and verified. **No RC2 artifacts should be released while GitHub Actions jobs fail before provisioning a runner.**
