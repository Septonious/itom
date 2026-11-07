# ITOM FIELD77 — MIME DATA fix, test candidate (NOT production release)

- Revision: `transport-s1-perf7-field77`; Android debug versionCode 9077 and name `0.9.0-dev2-field77-debug`.
- Fix: receive-side canonical charged MIME DATA size is derived from authenticated decoded body; physical MIME/UID remains unchanged for deletion proof, digest/AEAD/replay/grant checks are retained.
- Server diagnostic reports bounded, numeric DATA verification lengths.
- Reproduced failure: unchanged encrypted DATA body with additional provider CRLF previously yielded `DATA_STATE/encoded_size`; corrected end-to-end IMAP/TLS test delivers application bytes on same socket.
- Prior local validation: 19 Go packages PASS, vet PASS, 20 repeat E2E PASS, 3 race repeats PASS, durable journal/refund tests PASS. Normal synthetic 24+24 recorded 32.129/25.016 Mbps, RTT max 529ms, so strict 30/30 acceptance FAIL. These figures are not provider qualification.
- WARNING: No FIELD77 Android APK / real Rambler / device compatibility pass has been established. Production installation is not approved. Make full backups of `/var/lib/itom` and current server binary before a deliberate controlled VPS trial. Never delete durable databases or IMAP mail.

## Windows PowerShell build from Downloads
`python .\ITOM_TRANSPORT_FIELD77_SOURCE.zip all --expect-revision transport-s1-perf7-field77 --accept-android-licenses`

`dist/ITOM_FIELD77_debug.apk` and `dist/ITOM_FIELD77_linux_amd64` appear only after successful gates and APK identity validation.

## VPS test update
Upload `dist/ITOM_FIELD77_linux_amd64` to `/tmp/ITOM_FIELD77_linux_amd64` first. Then, as root on VPS:
`set -e; test -s /tmp/ITOM_FIELD77_linux_amd64; install -d -m 700 /root/itom-backups; cp -a /usr/local/bin/itom /root/itom-backups/itom-before-FIELD77; tar -C /var/lib -czf /root/itom-backups/state-before-FIELD77-$(date +%Y%m%d-%H%M%S).tar.gz itom; chmod 755 /tmp/ITOM_FIELD77_linux_amd64; /tmp/ITOM_FIELD77_linux_amd64 version; /tmp/ITOM_FIELD77_linux_amd64 install; systemctl restart itom; systemctl --no-pager status itom`

Important: This update is for deliberate testing only; do not deploy to production until actual bidirectional application data, durable journal recovery, MIME size and 30/30 network goals are verified.
