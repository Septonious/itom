# Original Rev3 testing pass and final continuation

The source-only testing pass at `itom-0.9.2-rev3-testing` was recovered from GitHub commit `9215b05`. Its executable archive SHA-256 is `ab86fbaf1d0429f98c826de045699e21fc02c3e30fd2821a58b0b2e6dff3a23c`. All 722 internal source-manifest hashes matched. The original archived runtime hash is `49016a6a2912acdc3a2418ff0649a85b9271243502157f16f999d900314bd726`.

## Field evidence agreement

The original investigation used the same uploaded Rev2 diagnostic captures. Rerunning its analysis against the current attachments produced a byte-for-byte equivalent parsed report. Fifteen accounts, 141871 retained client events and 120250 server events were confirmed. Client/server single-STATUS messages were 1460/773 and single-MAIL_RECEIPT messages were 4006/1846. Active bulk-window medians were 253440/130176 bytes, with repeated 64 KiB floors.

The uploads establish the user's SINGLE improvement and MULTI instability, including screenshots with 2274 and 3806 ms loaded download latency. Screenshot modes and timestamps are not individually labeled. Client RESET/CLOSED copy outcomes can also follow application closure. Unknown APPENDs at client shutdown must remain charged; they are not evidence that every earlier Speedtest failure was a carrier crash. Matched packet timings have uncalibrated cross-host clocks and do not qualify application RTT.

## Comparison of mechanisms

| Mechanism | Current intermediate investigation | Original testing pass | Final decision |
|---|---|---|---|
| Logical ownership | Tested OPEN affinity; no improvement | Already one shared authenticated actor | Keep the shared actor; discard the OPEN-affinity experiment |
| Stream fairness | Reproduced grouped stream queues and stream-ID admission bias | Interleaved prefixes and rotated scarce credit | Use original fairness and stopped-member isolation |
| Actor CPU | Profile exposed repeated map lookups during full-set sorting | Groups records and sorts stream keys | Keep original; additionally sort within stream buckets and index physical writer ownership |
| Speculative backlog | Fairness alone still allowed large stale queues | Bounds queued bytes by actual flight plus eight batch targets, within hard memory bounds | Keep original limits and protected short item/attempt slots |
| Pre-APPEND repair deferral | Intermediate changes were insufficient | Retains the same unspent NACK permit and keeps workers available | Keep original repair, predicate-wake and visibility-grace fixes |
| Feedback placement | Two fixed feedback writers and split-layout reserve experiments still failed load tests | Stable responsive stream affinity, rendezvous routing, independent control-service cost and bounded coalescing | Keep original adaptive routing; discard unsuccessful prototypes |
| Congestion samples | A delayed stream could reduce the whole session | Byte-weighted outstanding age with collective-stall guard; repaired ACKs do not sample stale residence | Keep original behavior and fixed delay objectives |
| Recovery frontier | Reproduced a worker handoff advertising unpublished bytes | Handoffs still extended attempted frontier | Extend recovery frontier only on committed/unknown physical outcome; preserve unknown debt and early receipts |
| Credit queries | Local queue limits could solicit peer credit | Queue-cap changes still used a generic zero-capacity predicate | Distinguish unavailable peer credit from local admission limits, preserving recovery when peer credit is lost |

## Evidence and limits

The original saved qualification completed all six 60-second cases with all 360 useful-overlapping probes and maximum RTT below 511 ms. Two MULTI cases met the whole/tail 100/100 Mbps target; the remaining MULTI case and all SINGLE cases failed throughput. These were stable laboratory results, not field qualification.

A fresh unchanged-original MULTI case in this environment completed 60 seconds at 92.104/88.552 Mbps upload/download, 576 ms maximum RTT, 60 overlapping probes, no timeout and no missed probe. The Rev2 baseline here stopped after seven measured seconds on a probe timeout. Intermediate fairness/placement prototypes also stopped early and are not release candidates. Their truncated averages cannot be compared as sustained rates.

The final release verification records new runs on the final source state. Original testing reports are preserved under `original_testing/` for provenance and are not represented as newly executed final checks. The 100/100 Mbps and sub-1000 ms criteria are unchanged. Missing, skipped, incomplete and failed measurements remain failures. Android/provider field testing is not available in this environment.
