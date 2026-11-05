# ITOM 1.1.0 MAILSTREAM-RC9 — regression recovery candidate

Update both endpoints. Install the signed APK in place preserving app data and
profile. For this first update from RC8, upload the matching server `.bin` to
`/tmp`, exit the old manager menu, and run the new binary's installer directly:

```sh
chmod +x /tmp/ITOM-1.1.0-MAILSTREAM-RC9-linux-amd64.bin
sudo /tmp/ITOM-1.1.0-MAILSTREAM-RC9-linux-amd64.bin install
```

This runs RC9's bounded installer and installs its new stop timeout before
stopping the old process. Manager option 18 can be used for subsequent updates.
The first stop of the already-stuck RC8 process may need the ten-second systemd
fallback; the new decoder close path is verified separately.

RC8 field captures show large queued bursts, window collapse and retransmissions,
plus cleanup stuck in maintenance_expunge. RC9 restores RC7's measured-rate
controller while retaining deadline-bounded stream admission. The artificial
RC8 infinite-capacity slow-receipt growth test is retired as release evidence;
retained history and actual provider reply ordering are now tested explicitly.

A 300-message EXPUNGE reply burst can fill the client's bounded response channel.
Previously, closing its socket could leave the decoder blocked on that channel,
preventing cleanup and shutdown from joining. RC9 cancels streaming delivery on
close/deadline and drains UID STORE and EXPUNGE replies concurrently while preserving the command pipeline. Manager service
commands have a 20-second bound and the installed unit a 10-second stop fallback.
Normal shutdown should complete promptly; a failed command is reported as an
error and does not pretend to reset a live ledger.

Automatic cleanup remains limited to owned messages, preserves unrelated mail
and never deletes folders. Empty folders intentionally remain. Leave the server
running after VPN disconnect for idle cleanup. No further manual mailbox cleaning
is part of this update. This is a testing RC; live-device/provider confirmation
is still required.
