# ITOM

ITOM transports TCP application streams through authenticated IMAP mailboxes. This branch contains **Rev6, internally qualified and packaged for field validation**.

- [Download APK and Linux binaries](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev6)
- [Release notes, reset instructions and installation](builds/0.9.2-Rev6/RELEASE_NOTES.md)
- [Rev6 source](archives/rev6/source)
- [Startup and idle-session analysis](archives/rev6/source/docs/rev6/REV6_ANALYSIS_2026-10-16.md)
- [Final qualification evidence](archives/rev6/source/docs/rev6/evidence/rev6_validation.json)

Rev6 bounds optional socket setup so stalled extra IMAP logins cannot hold authenticated activation for thirty seconds. Idle sockets use bounded NOOP keepalives that skip busy commands. LOGIN diagnostics preserve safe categories and known standard response codes. The settings introduction banner is removed.

Server manager **3. Mail accounts → 6. Reset accounts database** clears active credentials and retired account definitions after explicit confirmation with the service stopped. It preserves server/client identities, provider messages and accounting files. Fresh random account namespaces fence old journal IDs. Re-add accounts and export replacement profiles to every client before restarting. Reset is CLI-only; reset configurations require schema 6.

All ten internal gates passed on the exact packaged source, including four 34-case field suites, a 110-second fifteen-account idle scenario in every repetition, race checks, Android/native builds and lint, full core/IMAP checks and all six independent SINGLE/MULTI performance runs. See final evidence for exact results, limits and source identity.

This is a **prerelease**, pending physical-phone, live-provider and actual systemd-host validation. Accounts that first rejected LOGIN later accepted the same saved credentials in the field capture; the provider's precise policy and complete cause of field peer silence remain unconfirmed. The earlier Speedtest-app error and mobile fatal crash are not claimed resolved.

Update app and server together. Preserve profiles/state unless choosing the explicit account reset; after reset, replace all client profiles. Existing formatted messages, darker UI and guarded IMAP admin controls remain. GitHub Actions reassembles hashed release parts, verifies source and gate evidence, and verifies uploaded asset sizes and SHA-256 digests before publication. Captures, credentials, caches and signing keys are excluded.

Historical revisions and development evidence retain their original status and results.
