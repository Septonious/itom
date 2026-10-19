# Rev3 field findings and measured results

The uploaded Rev2 captures and the user report show that SINGLE improved while MULTI stalled and sometimes produced several seconds of loaded RTT. The screenshots show 3.29/0.00, 37.8/27.9, 39.5/27.3 and 17.5/11.9 Mbps download/upload; maximum displayed loaded download RTT includes 2274 and 3806 ms. They contain no mode/time markers, so exact screenshot-to-trace attribution is unavailable.

## What the captures establish

The client retains 141871 events from 11:35:32–11:37:39 UTC; the server retains 120250 from 11:35:52–11:38:05 UTC on October14, with fifteen accounts. The active server bulk window median is130176bytes and the client median253440bytes, with repeated64KiB floors. Control messages average1.29 client/1.54 server records;1460/773 messages respectively contain only one STATUS, and4006/1846 only a MAIL_RECEIPT. The client records43 WALLET_EXHAUSTED spend events. These observations are consistent with expensive feedback and repeated logical recovery.

The client records58 RESET/read and53 CLOSED/read copy outcomes plus11 IO_ERROR/write outcomes. App closure can produce these; the captures do not prove that every closure is a transport crash. All fifteen server carrier timeouts near11:38:04 occur about25seconds after the client trace stops, matching the existing watchdog after client shutdown. Independent DATA FETCH is present. Bulk C2S frame tracing lacked a counter in Rev2, so Rev3 adds stream-frontier and envelope-delivery observations. Cross-host clocks were not independently calibrated; one-way matched timings cannot establish application RTT.

## Reproduced mechanisms and verification

Regression tests demonstrated stream-prefix monopolization, stopped-member batch rejection, independent DATA occupancy migrating feedback, exhausted-item scheduling stopping siblings, pre-APPEND repair deferrals consuming permits, loss of the authorized repair request, self-extending visibility grace, repaired ACKs poisoning fresh delay samples, and an unused large learned window allocating a stale speculative actor backlog. A paired-stream routing test showed a four-path feedback prototype used only two paths per direction; the final rendezvous routing uses responsive accounts without that modulo collision. The tests also retain unknown physical debt, enforce committed/unknown repair bounds, and preserve collective outage and loaded-short latency guards.

A matched15-account/24-stream80ms laboratory Rev2 diagnostic failed after4 measured seconds on a loaded probe. The final Rev3 diagnostic with those parameters completes20seconds at84.5/108.1Mbps upload/download, maximum RTT374ms, all20 probes overlapping useful traffic. The old run is truncated; its partial throughput is not a sustained comparison. The final sixty-second repeats below are stronger evidence of stability. All measurements are laboratory observations, not Android/provider qualification.

## Final account and load diagnostics

Twenty-second useful TCP intervals, five-second warmup, fresh public supervisors over TLS/IMAP. Uniform cases declare80ms commands and a600Mbps provider link. Actual fsync remains enabled.

| Accounts | Streams per direction | Model | Upload Mbps | Download Mbps | Max loaded RTT ms | Completed / overlapping probes |
|---:|---:|---|---:|---:|---:|---|
| 10 | 1 | uniform | 104.399 | 81.605 | 399 | 20000ms / 20/20 |
| 10 | 8 | uniform | 63.137 | 59.992 | 399 | 20000ms / 20/20 |
| 15 | 1 | uniform | 140.824 | 105.893 | 366 | 20000ms / 20/20 |
| 15 | 8 | uniform | 67.935 | 60.503 | 392 | 20000ms / 20/20 |
| 15 | 24 | uniform | 84.476 | 108.069 | 374 | 20000ms / 20/20 |
| 15 | 24 | uniform-fragmented | 102.557 | 105.710 | 474 | 20000ms / 20/20 |
| 15 | 24 | wrapper-regular | 134.218 | 87.897 | 475 | 20000ms / 20/20 |

The fragmented case writes4KiB chunks through actual TCP; read coalescing remains part of production code. Regular wrapper stress uses pseudonymized Rev1 wrapper-duration samples, positive serialized server commit delay12ms/client control0.5ms, and excludes wrapper samples above500ms. It is an adverse diagnostic with stated assumptions, not a calibrated simulation of the new field captures.

Fifteen accounts improve SINGLE in this small uniform matrix:104/82Mbps at ten becomes141/106 at fifteen. Eight-stream MULTI download remains about60Mbps at both counts, while upload changes63→68Mbps. Thus fifteen are useful extra paths and may benefit SINGLE, but these measurements do not show a material eight-stream MULTI download gain over ten. The profile limit stays lifted. Compare10 and15 on the same phone/provider/server before choosing an operating count; fewer healthy accounts can be preferable to more slow ones. There is no matched account-count field trial in the uploads.

## Strict sustained qualification

Each case is fresh: fifteen accounts, sixty seconds measured after ten seconds warmup, twenty-four streams per direction for MULTI, actual useful byte-pattern verification, final thirty-second tail, and a strict1000ms deadline. Every case completes60seconds with60/60 overlapping useful probes, no missed probes/timeouts or useful-transfer gap failure. The target remains100/100Mbps for both whole interval and tail.

| Case | Sustained upload / download Mbps | Tail upload / download Mbps | Max RTT ms | Strict result |
|---|---|---|---:|---|
| single-1 | 159.340 / 97.059 | 144.494 / 101.502 | 448 | FAIL |
| single-2 | 140.784 / 99.724 | 123.487 / 109.567 | 492 | FAIL |
| single-3 | 143.135 / 93.616 | 150.051 / 97.518 | 510 | FAIL |
| multi-1 | 109.332 / 103.612 | 107.680 / 105.312 | 479 | PASS |
| multi-2 | 110.756 / 109.214 | 107.794 / 111.560 | 441 | PASS |
| multi-3 | 97.347 / 106.863 | 98.182 / 107.907 | 448 | FAIL |

**The full100/100 qualification fails.** MULTI repeats1 and2 pass; repeat3 upload97.347Mbps fails. All SINGLE repeats fail sustained download93.616–99.724Mbps. Thresholds were not relaxed. Rev3 resolves the reproduced stall paths and substantially improves the declared MULTI stability/latency tests, but consistently meeting the complete speed target remains unfinished. Real Android and provider Speedtest results have not been measured for Rev3.

Raw uploads, private account mappings, private profiling captures and signing keys are excluded from publication. The bundle includes numeric field reports, final laboratory evidence, earlier failures and build checks.
