# Rev10 development notes

Revision: `transport-rework2-rev10`. Baseline: the exact released Rev9 archive, SHA-256 `4b08e8000676a117b7c8ddbc438eb90bae9df4558292fea5a745f9c2044e0ca1`.

## Field diagnosis and repair

The supplied October 13 client and server traces show all ten accounts authenticating and completing bootstrap/coordinator negotiation. Only accounts 1 and 2 reach ready. Accounts 3–10 repeatedly wait for DATA grants; this is not an account login failure or a two-account limit.

The server's 192-message DATA profiles retain historical reservations of 85, 85, 176, 168, 170, 159, 159, 173, 162 and 172 messages respectively. The client cannot prove retirement of this historical allocation (wallet history is missing), so releasing that debt would be unsafe. The old recovery path nevertheless requires a fixed 32-message fast grant before any bulk grant. After preserving the twelve-message local reserve, accounts 3–10 have too few free slots for that fixed grant.

The recovery path now shares the free message slots between a bounded fast grant and a bulk grant. When the standard fast grant fits alongside bulk it remains unchanged. Both grants retain enough bytes for a maximum legal DATA body. At least two allocatable message slots must exist: one for each class. A new authenticated control type identifies small fast grants explicitly, instead of inferring their class from the old fixed quota shape. Both endpoints must update to Rev10; the required capability rejects incompatible peers.

No old grant is refunded, no physical capacity is increased and no cleanup proof is bypassed. Journal and wallet formats remain unchanged. Byte and message reserves remain enforced. Completely exhausted profiles still refuse startup. This repair restores usable capacity but cannot erase historical debt when proof is unavailable.

## Regression evidence

Before the repair, a fixture seeded with the ten field message counts reproduces exactly two ready accounts. After the repair, all ten pass. Boundary cases cover 178 held messages (one fast and one bulk slot still available), 179/180 held messages (insufficient slots for both), and a completely occupied 192-message profile.

A production supervisor integration fixture uses TLS IMAP, the same retained-message counts, fresh client wallets and actual TCP echo traffic on every account. It verifies ten ready accounts, twenty active transport directions, unchanged historical grants and a clean journal audit. This is a matched synthetic reproduction; real provider and Android device qualification remain required.

## Home layout

The server announcement is beside ITOM in the top header and scrolls within a bounded text area. It no longer consumes a separate full-width row. Portrait restores the full 286-dp connection ring whenever the width permits. The lower controls scroll on shorter screens or with larger system fonts, rather than forcing the ring to shrink. Landscape retains the compact two-column layout. App label is ITOM; version code is 9089; existing package identity is retained.

All Rev9 requested interface behavior remains: English text, four settings pages, mint/violet switches, generated missing SOCKS credentials, status-colored current account metrics and full diagnostics ZIP only. Backup accounts and a dedicated 4+1 allocation remain deferred.

## Build and update notes

The bundle includes newly compiled APK and Linux amd64/arm64 server binaries, executable complete sources and actual build/test reports. Update both endpoints together, preserve profiles and retain server journals. The included APK uses a different development signing key from the supplied Rev9 APK; Android does not accept it as an in-place update. Local rebuilding can reuse an existing installation's signing key. Private signing material is excluded from the public bundle.

See validation reports for checks actually run. Compilation and synthetic transport tests do not establish live provider throughput, phone VPN/TUN behavior or device layout qualification.
