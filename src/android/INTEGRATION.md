# Android integration — 1.5.1 routing development

Historical 1.3.0 RC3 UI is used as a verified starting point, not as a replacement
for RC20's lost Kotlin source. The current Go transport is unchanged.

Restored native routing is authoritative for the VPN/SOCKS paths. The Android
global policy includes Russian domain/IP direct routing, ad/tracker and malware
blocking, custom rules, and an offline native address checker. Existing bare
rules are converted into CSQTT syntax. The old priority metadata stays only for
encrypted vault compatibility; the Go classifier enforces the new block-first
policy. Domain/IP edits activate on reconnect. The private asset directory is
seeded atomically with verified RC20 data bundled at build time.

Native CheckRouting returns JSON with Action, Matched, Rule and Reason. RC20
DEX evidence confirmed policy JSON, directory, hostname and address argument
order and Action lookup. The historical Android Host.protect implementation
already binds protected sockets to the selected physical Network.

Development APK uses application ID org.itom.client.testing. Do not upgrade
the original signed RC20 with this candidate. The old Android UI, app-policy
migration, live TUN rebuild, automatic app classification and GeoData updater
still require completion. No production signing keys are read by CI.

Application routing now uses per-app Auto / Direct / VPN choices, complete shared-UID groups, and captured DownloadManager. Legacy policies can be migrated in the app picker. Changes rebuild only the TUN in the running service. Destination rules remain reconnect-only. Device validation remains outstanding.

## CI checkpoint

Android/native build at 3cdeaca8 passed (run 45834875690, artifact 15124538247). The qualification runner at the same revision did not start a benchmark: its independent Go module lacked the transitive x/text v0.42.0 checksums for the IDNA classifier. Pin the same dependency and checksum as the already passing native module, and validate the qualification dependency closure in readonly mode. No changes to transport scheduling, benchmark workloads, thresholds, or acceptance logic. The development APK is not a production release.
