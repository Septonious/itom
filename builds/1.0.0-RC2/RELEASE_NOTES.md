# ITOM 1.0.0 RC2 — account-stall routing correction

RC2 steers new work and cumulative stream feedback away from an account whose control writer has stalled. The RC1 captures show a single account with 10–16 second IMAP operations on both endpoints while siblings continue working. Stable feedback affinity and OPEN ranking could still select that delayed account; focused before/after regressions reproduce and correct this amplification.

The advisory routing threshold uses unloaded RTT and is bounded between one and three seconds. It does not reconnect a lane, replay an uncertain APPEND, move account-bound controls or release durable debt. Ordinary busy/quiet paths retain their existing behavior; last-resort routing remains available. RC1 authenticated early-NACK retention and bounded receive-stall recovery remain intact. There is no speculative increase to flight, socket or LOGIN limits.

Focused routing race checks and normal recovery checks passed. Multi-stream pause, persistent blackhole and capped-socket race cases also passed. A repeated broader race run hit an intermittent 3-second startup discovery timeout before fault injection. An isolated traced rerun passed, but its cause is unresolved; the failed run is included and all-targeted-gates remains false. Android tests, lint, native host checks, targeted vet and 45 launcher checks also pass. See VALIDATION.json and TEST_EVIDENCE.zip for exact scope and any qualification limits.

**RC2 is a testing prerelease.** The complete final internal matrix and real phone/provider follow-up are outstanding. The captures identify RC1 and have lost the earliest failed MULTI startup attempts to trace rotation. No new sustained strict 100 Mbps or jitter guarantee is claimed. Update both endpoints, preserving accounts, profiles and durable state. Version code 10001 retains the existing app ID and signer.

Includes APK, Linux AMD64/ARM64 binaries, executable sources, analysis, handoff and hashed evidence. GitHub Actions assembles, verifies and publishes the exact locally built artifacts; it is not an Actions compilation or a live benchmark.
