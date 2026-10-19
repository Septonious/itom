# ITOM 0.9.2 Rev8 handoff

## Status and deliverables

This is a testing prerelease, not a certified stable release. The user requested a fast, targeted verification pass and immediate APK, server binaries, complete source and GitHub upload. A full final-source test suite and physical phone/provider qualification have not been completed. Earlier Rev7 results remain historical evidence, not Rev8 certification. Consult STATUS.json, VALIDATION.json and TEST_EVIDENCE.zip for the exact successful and unsuccessful checks. A failed 100 Mbps or jitter measurement is not waived or relabeled as a performance pass.

Repository: https://github.com/Septonious/itom
Branch: `codex/rev8-stability-throughput`
Release tag: `v0.9.2-Rev8`
Editable project: `archives/rev8/source/`, or `itom/` in the executable source ZIP.

The full release ZIP contains the signed Android APK, Linux AMD64 and ARM64 executables, source archive, handoff, analysis, sanitized field correlation, validation evidence and checksums. GitHub Actions assembles the exact locally built bits from hashed archive parts, verifies their source fingerprint and native-library hashes, uploads them as a draft, checks every remote asset size and SHA-256, then publishes a testing prerelease. It does not claim to have rebuilt or field-tested those bits on GitHub.

## Changes from the supplied Rev8 source

The supplied branch was essentially the published Rev7 transport plus bounded diagnostic sampling of expected cumulative grant rollover. Keep that sampling: WALLET_EXHAUSTED before APPEND is an atomic lease rollover, not proof that an account or its journal is corrupt. Physical completion errors and other errors remain visible.

Rev8 now changes three latency policies. Once isolated short DATA/ACK samples establish the full-path baseline, the bulk governor permits 100 ms above that baseline instead of scaling extra delay with propagation RTT and retaining a 650 ms minimum. Uncalibrated paths retain the conservative bootstrap budget. Calibration still excludes repairs and traffic already sharing bulk load. This is a feedback threshold, not a guarantee that a provider can deliver every application exchange with under 100 ms jitter.

After two actual missing IDLE notifications followed by fetched rows, active-reader audit fallback is 50 ms instead of 150 ms, for the existing bounded 30-second interval. Quiet accounts retain their two-second cadence. A working notification path, an empty mailbox or merely having a TCP socket open cannot manufacture missing-notification evidence.

Control collection can now shorten its absolute flush deadline, never extend it when more urgent records arrive. Ready startup bursts and the existing work/record caps remain intact. The CLI revision also now reports Rev8; the supplied source left its command-line revision at Rev7.

The strict laboratory qualification path now requires 100 useful Mbps in each direction, without the historical ten-percent speed margin. Full-window, final-30-second and final-10-second useful-byte measurements all contribute. All scheduled probes, original TCP/carrier identity, integrity and post-load recovery checks remain independent requirements. Adjacent RTT absolute differences are reported as a separate laboratory jitter metric; they are not claimed to reproduce Speedtest's estimator.

Increasing bulk workers from eight to twelve was evaluated and rejected because the measurement worsened. The final source keeps eight workers, the original bounded handoff queue, physical APPEND limits and durable accounting. No provider account reset, mailbox purge, forced physical-debt forgiveness or source-of-truth change is included.

## Field evidence and interpretation

The new paired October 16 captures identify a Rev7-debug client. They contain 118,531 client and 116,690 server trace events covering fifteen accounts. There are 210 complete paired control echoes: median RTT 358.39 ms, p95 691.92 ms, maximum 2,145.60 ms. Client enqueue-to-seal p95 is 53.31 ms, maximum 289.42 ms; server receive-to-reply-seal p95 is 111.27 ms, maximum 297.69 ms. Much of total RTT is outside these queue intervals. One-way differences are not treated as synchronized network measurements.

Application resets coexist with connected carriers. Generic client IO_ERROR and remote socket RESET records do not prove a unique Speedtest failure cause. A large cluster of local application resets is also consistent with the test cancelling its parallel sockets. Expected quota rollover is not a basis for throwing away charged debt. The precise Speedtest failure remains unconfirmed without a matching field pass of this candidate. Raw diagnostic ZIPs, target addresses, account identifiers, provider passwords, private signing material and profiles are excluded from publication.

