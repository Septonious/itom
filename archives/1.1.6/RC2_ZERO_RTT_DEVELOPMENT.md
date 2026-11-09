# ITOM 1.1.7 RC2 development checkpoint — zero RTT regression priority

This is **development source**, not a tested or published release. Do not ship
client/server artifacts until all gates below pass.

## Baseline and goals

- Published reference: `v1.1.6-rc1`, commit
  `a134e4a05f8aa7cb83cf455129bd633c5c90c96e`.
- Candidate starts at unreleased 1.1.7 work
  `94404877b311fb6bf1bcfae2394eb07f9e12b36a`.
- Absolute policy: **no measured RTT increase in any tested network, direction
  or idle/loaded/after-load phase**. p50/p95/p99/max all matter; mean gains do
  not excuse tails or sporadic stalls.
- Maximum sustainable application throughput may not drop more than **25%**
  relative to the same profile's published-RC1 baseline.
- The main target is poor, asymmetric mobile (<20 Mbps), not a record home
  speedtest; preserve the ~350-400 Mbps home field reference as a guardrail.

## Added to this candidate

1. **Removed activity-dependent 3/8/16 ms polling backoff.** It could add up
   to 15 ms to the earliest incoming packet after idle and thus violates
   the policy even if it reduced radio requests. The original 1 ms interval
   remains in every state. No new timer, additional IMAP RTT or scheduling
   wait is introduced.
2. **ASCII85 MIME lines extended 480 -> 960 octets**, within the 998-octet
   line limit. Existing sender address, mandatory 7bit encoding, ITOM4
   subject, session authentication and parser are preserved. This reduces
   wrapping overhead without increasing packet coalescing residence.
3. **Cross-version MIME tests:** immutable RC1-format encoding, random
   authenticated-envelope round trips, per-line length bounds, and
   measured bytes for a 256,000-byte bulk sample.
4. **Strict three-trial A/B acceptance:** new
   `qualification/gate_rc117_zero_rtt.py` checks *every trial* and every
   available RTT minimum/median/p95/p99/maximum for idle/loaded/after-load
   against published RC1, rejecting any positive delta. Upload and download
   throughput must be >=75% in every trial, and best throughput across
   trials must preserve the same floor. Unit tests cover pass/reject cases.
5. **Expanded the A/B matrix** with a low-rate 8/2 Mbps case, in addition
   to 20/5, 160/90 and 500 Mbps profiles. New PR code remains quarantined
   from the released stable branch.

## Inherited from earlier unverified 1.1.7 development

- Retriable TLS/IMAP transport failures are separated from certificate and
  explicit login rejection. A dropped TLS connection and an actual invalid
  server certificate have dedicated regression tests.
- After prior cleanup failure, a bounded provider mutation retry path uses
  an 18-second deadline while preserving storage proof/accounting.
- An initial shorter valid MIME sender address saves 16 bytes per APPEND.
- Experimental RTT ranking and immediate-feedback behavior that increased
  RTT tails were already reverted/disabled upstream.

## Do not claim qualification

Recent GitHub Actions jobs finish without any reported runner start or step
logs. That is a CI provisioning/blocker, **not** a source-test success or a
measured performance result. No new APK or Linux binaries are claimed here.

Required before publishing:
- Restore CI runner execution.
- Full Go core, native, race and recovery suites; Android tests/lint/build.
- Multiple source-pinned paired synthetic A/B profiles with all strict
  gates green; collect actual application RTT values and IMAP/TLS wire bytes.
- Repeat loss (0.5%, 2%, 5%), outages and network handover tests using real
  fault injection (current benchmark cannot simulate these faithfully).
- Physical Android + IMAP provider trial, including authenticated server
  matching, provider retention cleanup and safe update/restart.
- Signed and hash-verified Linux AMD64, ARM64 and Android APK built from the
  exact qualified revision.

## Engineering constraint

Strict zero RTT change **cannot be proved universally** from finite noisy
benchmarks. Treat this as a conservative fail-closed measured acceptance gate
plus structural checks that forbid newly introduced waits. If even one measured
RTT metric increases, investigate; do not promote this candidate as compliant
until reproducible evidence clears the regression.
