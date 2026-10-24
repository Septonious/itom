# Session transport v2: unqualified source

The old runtime has been removed. This branch contains the independent
replacement implementation in progress and new tests. It cannot carry live traffic yet:
client/server startup returns `V2_NOT_READY`, and packaging is disabled.

See [implementation status](../SESSION_V2_STATUS.md) and
[current source check results](../experiments/session-v2/POLL_CHECK_RESULTS.json)
([first iteration](../experiments/session-v2/SOURCE_CHECK_RESULTS.json)).
Use `python3 tools/check_session_v2.py --go /path/to/pinned/go` for source checks.
These checks do not establish the application latency/throughput release gates.

The text below describes the historical DEV9 build, not this replacement.

---

# ITOM 1.1.0 DEV7 — experimental transport development

DEV7 reduces shared control/cleanup command round trips and uses remaining historical-journal capacity for two full bulk BODYs where they fit alongside a complete FAST body. It preserves all charged historical debt, quota ceilings and journal proofs. New field-capacity tests retain the 100 Mbps and no-stall requirements; field bandwidth restoration remains unverified.

This source starts from frozen RC2/1.0.0. It adds demand-scoped receive timing,
per-socket quiet/burst coalescing. The fast-lease rollover experiment is
deferred separately and is not applied in this source. It is not stable or field qualified. The first matched
comparison improved idle/light RTT; retained-state100Mbps and a SINGLE loaded
RTT tail failed unchanged gates. Consult the sibling1.1.0 experiment report.
Android stays org.itom.client with version code10107. Preserve app data,
wallets, server identity and journals. Build artifacts record their commit and
qualification status. Existing operational documentation follows as reference.

# ITOM 1.0.0

The user-selected final release restores the RC2 transport. No post-RC2 transport or quota/journal/wallet policy is applied. Release identity strings and Android packaging/signing safeguards are updated. Android application ID remains org.itom.client; versionCode 10014.

Historical RC2 used org.itom.client.dev.rev7. This build keeps the current package identity instead. A matching signer is still required to update an installed org.itom.client app. Preserve the app/profile, wallets, server state and journals; never uninstall/clear data merely to bypass a signer error. Rollback across later retained-state formats is not automatically certified.

The original qualification thresholds are unchanged. Selecting RC2 as final is the user's release decision based on field experience; it is not a claim that all historical synthetic or live-provider gates passed. Transport rework and idle/light RTT experiments belong to 1.1.0.
