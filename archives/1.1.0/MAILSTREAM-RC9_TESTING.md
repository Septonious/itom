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

## Final verification

Frozen source: `a4bb15b3a56b9f8098070eabc71029940682989d`.
[Matched build, safety, throughput and retained-history checks](https://github.com/Septonious/itom/actions/runs/41996109785);
[independent source acceptance](https://github.com/Septonious/itom/actions/runs/41996109946).
The final local normal/race/native/SDK/vet gate also passed. The downloaded amd64
server passes three native startup/reconnect cycles at 30/30 roles and three at
45/45, including two server restarts and verified manual-stop capacity resets.
Android passes 93 domain tests, 8 app tests and lint with zero errors. Version
code is 10134 and the existing Android signing certificate is preserved. Both
JNI hashes/revisions, both server binaries and the source archive are verified.

The frozen RC8 reference reproduces both an undrained EXPUNGE close hang and a
300-message retained-control cleanup hang. The candidate fixes both, preserves
the existing bounded stream-admission behavior, and passes the manager timeout
check. A serial-cleanup prototype passed reliability but failed the second-cycle
upload gate at 69.38 Mbps; it was not released. The final implementation drains
both replies concurrently and keeps the mutation/audit pipeline.

These are verified HTTPS transfers through a controlled TLS IMAP provider,
not live Rambler or Android-device measurements:

| Model / streams / cycle | Upload Mbps | Download Mbps | Max idle/post RTT ms | Max loaded RTT ms |
| --- | ---: | ---: | ---: | ---: |
| ordinary / 1 / 1 | 174.0 | 196.7 | 120.1 | 389.0 |
| ordinary / 4 / 1 | 186.8 | 181.0 | 90.0 | 332.0 |
| ordinary / 4 / 2 | 102.5 | 196.9 | 90.0 | 332.0 |
| ordinary / 32 / 1 | 241.3 | 302.7 | 125.5 | 539.2 |
| 300 retained messages / 4 / 1 | 225.6 | 294.7 | 110.1 | 458.7 |
| 300 retained messages / 4 / 2 | 104.7 | 141.0 | 110.1 | 458.7 |

The 7 Mbps shared-wire case completes at 4.72 Mbps
upload and 4.51 Mbps download, including download
cancellation and fresh HTTPS recovery. Maximum idle/post RTT is
89.2 ms and loaded RTT is 1239.5 ms.
All accounts remain connected. Full-phase 100 Mbps on the capable path, 300 ms
idle/post RTT and 2000 ms loaded RTT checks pass in the final hosted cases.
Half-second-pause and rolling-five-second-throughput flags still miss in the
1-/4-stream and retained-history cases; all raw flags remain in the evidence.
This does not claim that every sustained/ramp/pause goal or live-provider goal
has been met.

The retained-history model seeds 300 old messages in every control folder and
emits EXPUNGE during UID STORE, then completes two full upload/download cycles.
No account connection rejection occurs. Current field performance still needs
confirmation. Enable diagnostics before connecting, test both MULTI and SINGLE,
then disconnect/reconnect and check stop/update responsiveness. Empty folders
are expected; no additional manual mailbox cleaning is part of this release.
