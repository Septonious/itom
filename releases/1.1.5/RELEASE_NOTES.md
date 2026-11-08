# ITOM 1.1.5 — stable and complete

ITOM 1.1.5 is the completed, field-accepted ITOM transport. All main goals are met and surpassed, as confirmed by the user on 8 November 2026 (Europe/Moscow).

The user reports peak speeds of 290 Mbps download / 340 Mbps upload with approximately 200 ms loaded RTT. No line disconnections were observed; the connection remained usable over time for Speedtest, Telegram and YouTube. These are user-reported field results.

This stable release preserves the exact APK and Linux AMD64/ARM64 binaries accepted as 1.1.5 testing. Their SHA-256 hashes, application ID, Android version code and signing certificate are unchanged. Existing 1.1.5 testing installations already run the completed transport.

Validation passed all five qualification jobs: full normal/race safety, a three-minute real HTTPS video test with a provider keepalive failure, consecutive 15-account/60-line native transfers, Android release compilation with both JNI architectures, 102 Android tests, and fatal lint. Release assets are downloaded again and checked before stable publication.

Idle writer failures recover at an application APPEND fence while selected readers, authenticated sessions, generation state and mailbox debt survive. The qualified controller, protocol, four-folder layout, four real connections per active account, mailbox limits and server-only cleanup remain in place. No mailbox migration or purge is required for promotion.

The existing connection budgets apply: two active users sharing each provider account require sixteen allowed provider connections per account; the ten-connection default admits one active user.

Install the matching app and server from this release. Users already on the tested 1.1.5 packages do not need to reinstall or replace their binaries. The saved signing key is retained privately; private keys and credentials are never included in release assets.

Source commit: 0393b34b281699861b77da247066118dae807fc5
Build qualification: https://github.com/Septonious/itom/actions/runs/42458089376
Testing publication/readback: https://github.com/Septonious/itom/actions/runs/42458762207
