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
6. **Provider TLS alert recovery:** retry RFC 8446 `close_notify` (0),
   `internal_error` (80) and `user_canceled` (90) through the existing
   bounded startup reconnect schedule. Certificate validation and explicit
   identity/protocol rejection remain strict and permanent; focused tests
   cover both decisions. This changes failure recovery, not the healthy
   connection's per-packet scheduler.

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

## Continuation: lower overhead without delaying control delivery

7. **Reuse the standing control-poll timer** instead of allocating a new
   `time.After` per empty UID FETCH. Timer reset is performed at the same
   point as the released 1 ms wait; the timer is stopped when an IMAP
   command or cancellation wakes the reader. Go's post-1.23 timer semantics
   prevent stale ticks from leaking between resets. A focused test and
   benchmark exercise command wakeups and allocation count.
8. **Omit redundant UID data-item requests from UID FETCH**, leaving the
   absolute ascending UID range, mandatory `RFC822.SIZE`, literal read
   bounds, UID-range checks and deletion/accounting proof unchanged.
   RFC 3501 and RFC 9051 require that UID FETCH responses contain the
   UID even if not explicitly requested. A verified-TLS memory-server
   integration test checks both a normal data response and a forward range
   beyond the mailbox tail. This reduces control request bytes but does
   not introduce SEARCH/NOOP or a separate round trip.
9. **Handle temporary IMAP LOGIN provider status codes:** `UNAVAILABLE`,
   `INUSE`, `LIMIT`, and `SERVERBUG` now enter the existing bounded
   startup retry policy. Explicit `AUTHENTICATIONFAILED`,
   `AUTHORIZATIONFAILED`, `NOPERM` and certificate errors continue to
   fail closed. A live TLS fixture tests two forced radio disconnects
   followed by a successful authenticated connection.
10. **Validation-only workflow** `.github/workflows/itom-117-rc2-safety.yml`
    tests Go/core/native, a race-focused RC2 cohort, both Linux targets,
    Android and Python acceptance gates. It does not invoke GitHub release
    APIs, upload production binaries, or use signing secrets.

### Local standalone microbenchmarks (NOT the ITOM transport RTT suite)

Executed independently of GitHub Actions on a local x86-64 AMD EPYC
container using Go **1.23.2**, not the pinned Go 1.27.1:

| Microbenchmark | Reference | Candidate |
| --- | ---: | ---: |
| Timer allocation / operation | `time.After`: 3 | reset/stop reusable timer: 0 |
| Timer benchmark time / operation | about 291–304 ns | about 46 ns |
| MIME bytes for 65,536 random bytes | 82,414 | 82,244 |
| MIME bytes for 256,000 random bytes | 321,486 | 320,820 |

The MIME smoke test decoded both envelopes to the original random payload.
The timer allocations and CPU times are compiler/runtime-specific and only
demonstrate potential memory/CPU savings. They **are not measured network
RTT**, actual ITOM throughput, mobile power use or provider interoperability.
The UID command-byte savings likewise require the real TLS IMAP fixture
and physical-provider check before being called field-qualified.

### Runner blocker confirmed

After adding the separate validation-only safety workflow, both `go-safety`
and `android-validation` jobs failed before their first step; the RTT A/B
matrix continues to fail the same way. A rerun of an earlier failed job
also failed without execution or downloadable logs. No passing full-source
test, measured RC2 RTT distribution, signed APK or new server executable
is asserted from these failures.
