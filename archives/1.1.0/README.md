# ITOM 1.1.0 replacement transport — unqualified source

The current development branch contains an independent transport rewrite.
Production startup returns `V2_NOT_READY`; packaging remains disabled. The
250ms idle/light RTT and sustained 100Mbps targets are not established.
See [implementation status](SESSION_V2_STATUS.md), the
[structural plan](STRUCTURAL_REWORK_PLAN.md) and
[latest source checks](experiments/session-v2/REQUEST_TRACE_CHECK_RESULTS.json).

[Per-account runtime integration](PER_ACCOUNT_RUNTIME.md) now negotiates
original issuer identities and assembles the multi-account TLS/HTTP service.

[Existing-profile planning](PROFILE_SERVICE_PLAN.md) preserves per-account
journal identities and retained folder debt before service selection.

[Selected-lane roster negotiation](SESSION_ROSTER.md) now discovers DATA issuer
identity through accounted mail before the runtime.

[Session authority integration](SESSION_AUTHORITY.md) describes the accounted
grant/fence/status/retirement channel now used by the lab runtime.

[Working field-test release preparation](FIELD_RELEASE_PREPARATION.md) tracks the
remaining runtime/bootstrap, persistence, native and qualification dependencies.

The DEV7 description and downloads below are historical evidence, not the current
rewrite or its qualification results. Stable 1.0.0/RC2 remains separate.

---

# Historical ITOM 1.1.0 DEV7 experimental testing RC

DEV7 reduces shared IMAP command waits and makes two complete bulk bodies usable where retained quota capacity permits, preserving a complete FAST batch and every historical charge. It also fixes an admission diagnostic-snapshot data race. Stable 1.0.0/RC2 remains unchanged.

[GitHub downloads](https://github.com/Septonious/itom/releases/tag/v1.1.0-dev7) · [Testing notes](DEV7_TESTING.md) · [Build checks and hashes](DEV7_BUILD.json) · [Field analysis](experiments/dev7-field-capacity/FIELD_ANALYSIS.md) · [Matched results](experiments/dev7-field-capacity/RESULTS.md) · [Retained failures](experiments/dev7-field-capacity/KNOWN_FAILURES.json).

The captured-capacity test averages improve from DEV6's 92.7/68.7 to **119.1/91.3 Mbps MULTI**, and 91.9/69.4 to **116.8/104.1 Mbps SINGLE**. All twelve phases complete, with fewer stalled windows; only **4/12** meet every strict criterion. Original sustained qualification is **17/20 versus RC2 19/20**; original short sequences are **48/48 versus RC2 42/48**. Fifteen source/fixture manifests were verified. Empty/less-indebted journal rates above 200 Mbps cannot predict installed Rambler performance.

Full normal Go/vet, accounting/mailbox races, new concurrent admission regression, original mixed-load race test in CI, Android unit tests/lint and native builds pass. The original throughput, loaded RTT and stall failures remain intact; local race-instrumented loaded chat also has a deadline miss without a data race. This is not fully qualified or a guarantee of real-provider 100 Mbps or improved idle RTT. No numerical qualification threshold was relaxed.

Update both the signed APK and server in place. Preserve app wallets, server identity and journals. Published DEV5/DEV6/DEV7 APKs use the same persistent signing certificate.

[Request latency work](REQUEST_LATENCY_WORK.md) separates receive and maintenance
ownership and adds bounded wire propagation tests. The light-load gate remains
failed; source integrity success does not qualify an APK/server release.
