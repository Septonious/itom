# ITOM Rev4 field repair — unstable work in progress

This branch investigates the 2026-10-15 Rev4 transport regression and whole-app
mobile startup crash. Candidate source is in `archives/rev4/source`.

The sparse UID history DATA stall is reproduced and repaired. Application,
transport, native and Android validation is in progress. The phone crash has
no fatal stack yet, and its cause remains unconfirmed. No replacement release
is published from this branch.

Existing archive manifests, build bundles and APKs describe the previously
published Rev4, not this unfinished candidate. Do not use them as evidence that
this branch is qualified. All required internal checks must pass before a new
release; the speed/RTT tolerance is 10%.

See `archives/rev4/source/docs/rev4/REV4_FIELD_REGRESSION_2026-10-15.md`.
