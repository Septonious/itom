# ITOM RC5: strict 10-account duplex investigation, 2026-10-17

**No result in this document passed sustained simultaneous 100/100 Mbps. RC5 is not a release.**

## Identical baseline requirements

Disposable laboratory simulator, real Go1.27.1 supervisors/TLS/IMAP fixture, 10 accounts, provider 10 active IMAP logins per account, modeled 80ms provider command cost and 600Mbps physical network, GOMAXPROCS=4 under 4-CPU quota, 24 TCP streams each direction, concurrent 60-second user payload measurement, 30 loaded echo probes and same/fresh socket verification. Values are full-window goodput and max loaded RTT, not peak throughput. Every case below is a *single* modeled-provider run; differences are not statistical claims. Use source/documented configurations to reproduce. All nonbaseline runtime modifications were made only in disposable local source and were NOT merged into RC5.

| Trial | Download Mbps | Upload Mbps | Upload final 30s | Upload final 10s | Max RTT ms | Status |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| 12MiB journal reference | 74.012 | 152.878 | — | — | 498 | FAIL down |
| Server24MiB / client12MiB, baseline ACK telemetry | 106.011 | 35.761 | — | — | 401 | FAIL up |
| Same 24MiB profile, repeated-fast-cut suppression on authenticated fast+bulk ACK | 106.269 | 39.099 | — | — | 503 | FAIL up |
| Same 24MiB profile, recovery window/4 on recent confirmed DATA→ACK with short path healthy | 110.026 | 26.468 | 16.506 | 14.234 | 595.973 | FAIL up |
| Same 24MiB profile, client physical DATA batch limited to 128KiB | 110.258 | 23.418 | 6.335 | 9.332 | 419.494 | FAIL up |
| Same 24MiB profile, 4-server/6-client physical sessions (rather than validated 5+5) | 103.717 | 19.053 | 9.507 | 8.625 | 706 | FAIL up |
| Total AccountSessions=5 (2-server/2-client actual), both 12MiB | 41.117 | 14.143 | — | — | 1011 | FAIL both/RTT |

All completed 60-second runs had 30/30 overlapping loaded probes, zero misses/timeouts and verified original/fresh TCP operation after load, **but strict duplex speed failed every case**. The degraded total-five-session case additionally violates <750ms loaded RTT. Larger server DATA journal profiles 20/24/32MiB are not allowed by committed production compatibility rules and MUST NOT be silently introduced.

## Verified new insight: stale outstanding age versus fresh ACK turnaround

The development source now records confirmed physical-to-logical DATA ACK age only when an original physically committed attempt is covered by a real authenticated logical ACK. When a fresh control path recovered under a server24 journal, the client still carried older outstanding logical ranges at 1.3–2.6 seconds while recently ACKed physical DATA often completed in 200–300ms and short ACKs had returned to ~300–350ms. Slow whole-session upload-window recovery followed a real fast-path stall. However, **relaxing repeated cuts or accelerating recovery alone did NOT stabilize upload**: new pressure episodes appeared and it collapsed again. Consequently none of these controller overrides was merged. The physical/ACK diagnostic counters and focused proof-origin tests were merged because they have no delivery/credit authority.

The same modeled provider had 10/10 authenticated per-account connections (no cap denials) with the original 5+5 split; forcing a 4+6 split used peak 9/10 but further harmed upload. Provider physical session starvation from oversubscription is ruled out in these fixtures.

## Definitive current gate

Release/publish/tag must remain blocked until **all** mandatory 10-account SINGLE/MULTI cases on fresh and retained journals (3 repetitions per mode/state, 12 total) achieve >=100Mbps useful payload UPLOAD and DOWNLOAD over the complete measurement and declared sustained tails, every loaded RTT <750ms, zero missed/timeouts, original/fresh TCP socket survival. Separately confirm the result on the actual phone with real provider and Speedtest target. The aspiration remains 125/125Mbps sustained.

**Next required code investigation:** measure ACK *stage* time across sender physical APPEND, peer DATA BODY discovery, logical ACK seal, control APPEND, peer control BODY discovery, and authenticated frontier application. Use existing trace `frame_sealed` / `frame_rx` counters and per-endpoint file traces, matched per-account/epoch; avoid logging payloads or secrets. Test real queue/actor/IMAP stages under the existing 12MiB profile first. Only optimize a confirmed stage and require zero safety regressions in quota/unknown-APPEND/replay/TLS/cleanup integrity. Do not tune per-account session or bucket caps again without proof.
