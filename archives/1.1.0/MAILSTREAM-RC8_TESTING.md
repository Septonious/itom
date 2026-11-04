# ITOM 1.1.0 MAILSTREAM-RC8 — upload and burst admission candidate

Update both endpoints; install the signed APK in place, preserving app data and
profile. Upload the matching server `.bin` to `/tmp` and use manager option 18.

RC7 field traces show 78 immediate stream-capacity refusals and a saturated
512 KiB upload flight with 400–600 ms DATA receipt timing despite fast control
pings. RC8 waits for stream capacity within the caller deadline and retries
remote admission when a preceding close has not arrived. Upload flight probes
once per bulk observation round while demand exceeds the window; queue growth
bounds the probe on weak links. All memory and stream limits are retained.

RC7 cleanup, persistent accounting, reconnect behavior and DNS policy remain.
Automatic cleanup never deletes folders. Leave the server running after VPN
disconnect for idle cleanup. Real provider/device qualification remains pending.
