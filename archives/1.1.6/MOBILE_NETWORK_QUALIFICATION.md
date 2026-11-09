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

The fixture currently supports rate/latency restrictions and temporary rate dips. It **does not yet implement real transport loss or IP handovers**; those require separate fault-injection infrastructure, not fabricated successful CI claims.

**Prioritize:** transfer completion, session survival/reconnect, battery wakeups, IMAP commands and total wire bytes, correct mailbox retention/cleanup, loaded RTT p95, then median RTT and peak throughput. A speed gain is invalid if it makes the VPN unusable after a weak-radio interruption or increases retained storage without audited cleanup.

## Current candidate techniques

1. Idle control receivers currently issue empty UID FETCH with a 1 ms gap. The candidate uses a **shared user-activity clock** and bounded 3/8/16 ms gaps only after sustained quiet. It must return to 1 ms while any user control or data traffic is accepted or received.
2. Keep mandatory 7bit ASCII85. The new valid MIME sender address reduces each APPEND by 16 bytes with no wire-version change. Evaluate future BINARY only if **actual authenticated IMAP capabilities** show support; BINARY requires provider interoperability, integrity, storage and rollback qualification.
3. Distinguish TLS/LOGIN **transport** EOF/timeouts from permanent TLS verification or credential rejection. Existing exponential startup backoff must handle temporary failures without weakening certificate checks.
4. Account-specific cleanup retries can use a finite **18-second** mutation deadline *only after* prior failure, when their page remains restricted to 32 messages / 4 MB. Never refund mailbox debt without authoritative physical deletion proof.
5. Keep the released primary round-robin placement and ordinary feedback batching: both immediate-prompt-feedback and RTT-aware hedging produced worse loaded tails in paired synthetic trials.

## Known incomplete work

- Established-session IMAP TCP failures can still require a new VPN session. Android currently records network changes and can terminate a failed session, but automatic controlled restart after an interrupted active session needs its **own** design, tests and policy checks.
- The user's server diagnostics showed nearly **787 MB** of retained mailbox data, 11/15 accounts in storage pressure and multiple quarantined cleanup operations; verify finite clearance on live providers before any stable promotion.
- Instrument radio uplink and downlink *including repeated IMAP polling and TLS framing*, not just encoded payload bytes. Android current diagnostic snapshots can report capability booleans and poll interval but do not yet count physical TLS bytes.
- GitHub Actions runner allocation was failing before executing any steps at this development head. Do not label the candidate validated or publish an APK from it until runner jobs, full race tests, native+Android checks and paired mobile tests complete successfully.
