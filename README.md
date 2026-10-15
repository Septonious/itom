# ITOM Rev4 field repair — unstable draft

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
was tested. This branch stays unstable and no replacement release is published.

Existing archive manifests, build bundles and APKs describe the previously
published Rev4, not this candidate. No new APK or binary is published here.
All required internal checks must pass before a release; speed/RTT tolerance
remains 10%.

See the [field investigation](archives/rev4/source/docs/rev4/REV4_FIELD_REGRESSION_2026-10-15.md).
