# ITOM Rev4 field repair — unstable testing

Candidate source is in `archives/rev4/source`; review [draft PR #1](https://github.com/Septonious/itom/pull/1).

The deleted-UID DATA stall, SEARCH-index lag, sparse-revisit starvation and
needless dense-page SEARCH turns have regression tests and repairs. Android
callback exceptions are contained through session cleanup.

All final internal gates passed on qualification source
`ec20fb8f08dd65552ffb2db9f328d306b90178e01d28e5a987c46dbbb7f74da8`:
834 core test events / 19 packages; 3×14 application cases; 14 cases with 12 ms
server commit delay; race checks; native and Android checks; 71 domain and four
callback tests; and all six fresh SINGLE/MULTI speed/RTT cases. Lint: zero
errors, one existing warning. See the [qualification report](archives/rev4/source/docs/rev4/evidence/field_repair_validation.json).

The reported whole-app mobile startup crash has no fatal record in the supplied
captures. Its cause remains unconfirmed; no physical phone or real provider
was tested. This branch stays unstable; the field crash is not confirmed resolved.

The repaired APK, Linux x86-64/ARM64 binaries, source ZIP and checksums are packaged
for [field repair testing 1](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev4-field-repair-testing.1).
GitHub Actions assembles the exact qualified bits from
`archives/rev4-field-repair-testing`, verifies every asset and its qualification
evidence, and publishes an explicitly **UNSTABLE** prerelease. See
[install notes](builds/rev4-field-repair-testing/TESTING_NOTES.md).

Existing manifests and bundles under `archives/rev4` outside its `source`
directory still describe the earlier published Rev4. Use the new testing package
for this repair. App/server version strings remain Rev4; distinct filenames,
checksums and `STATUS.json` identify the repaired build.
All required internal checks must pass before a release; speed/RTT tolerance
remains 10%.

See the [field investigation](archives/rev4/source/docs/rev4/REV4_FIELD_REGRESSION_2026-10-15.md).

The [new Wi-Fi/mobile comparison](archives/rev4/source/docs/rev4/REV4_WIFI_MOBILE_COMPARISON_2026-10-15.md)
documents paced Wi-Fi BODY transfers and account `a05` staying at zero grants.
It adds no production changes; matching server quota evidence is needed before
choosing a repair for the inactive account.
