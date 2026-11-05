# ITOM 1.1.0 MAILSTREAM-RC10 — testing candidate

RC7 remains the best field baseline. RC10 keeps its measured-rate delivery
controller and addresses maintenance interference exposed by RC9 field logs.
It does not claim live-provider performance qualification.

## Changes

- Stop the once-per-second control-report feedback while historical recovery is
  incomplete and capacity has headroom.
- Defer historical namespace inventory and abandoned-folder sweeps during an
  active VPN, unless historical debt actually threatens the finite capacity.
  Current DATA backlog uses its own audited collection path. Real control-byte
  pressure can still request prefix collection.
- Exchange authenticated pressure/clear notifications in both directions.
  Notifications never refund debt; verified recovery proofs still do. This
  handles a manually reset server paired with a full client, and the reverse.
- Drain ordinary APPEND replies before acquiring the shared mutation lease,
  allowing another ready account to collect during that wait.
- Keep ready DATA collection turns from being donated to historical sweeps.
  Amortize exact DATA collection over 128 bounded containers instead of 32,
  without delaying individual small writes or increasing the 64 MiB account cap.
- Preserve interruptible IMAP shutdown, bounded manager operations, stream
  admission, direct DNS behavior and the existing Android signing identity.
- Add diagnostics for deferred recovery and local/peer historical pressure.

No folder DELETE is performed. Empty folders are expected. After VPN disconnect,
owned retained messages are collected automatically while the server remains
running; this release does not require another manual mailbox clean.

## Install both matching endpoints

Install `ITOM-1.1.0-MAILSTREAM-RC10.apk` in place, preserving app data and profiles.
Upload the matching `.bin` to `/tmp`; manager option 18 chooses the newest file
whose name includes ITOM/itom. A direct update also works:

```sh
chmod +x /tmp/ITOM-1.1.0-MAILSTREAM-RC10-linux-amd64.bin
sudo /tmp/ITOM-1.1.0-MAILSTREAM-RC10-linux-amd64.bin install
```

If the installed old manager hangs on update/stop, exit it and execute the new
binary directly as above. Retention pressure notifications require both new
endpoints. Android version code is 10135; application ID and signer are unchanged.
Manual stop/restart/update and purge retain the existing local-capacity reset
behavior; a failed stop cannot reset accounting beneath a running service.

## Qualification

The new local stress prototype adds 40 old DATA folders and 300 old control
messages per account, 20 seconds of idle observation, variable 10–50 ms client
and 20–40 ms server IMAP RTTs, and 40 ms account-wide metadata service costs.
The metadata interference is a stress hypothesis, not a measurement of Rambler
internals. Frozen RC9 fails a later upload. Earlier prototypes either failed a
later download or completed with a 46 Mbps later upload; they were not released.
The 128-container prototype completes three speedtests: upload 279.5/150.4/104.8
Mbps, download 174.6/175.5/257.0 Mbps, maximum idle/post RTT 185.1 ms and loaded
337.5 ms. All 30 roles remain connected and no active historical LIST occurs.
These are prototype synthetic results; final exact-source hosted gates and
artifact checks are required before publication.

Short-pause and rolling-five-second-throughput targets still miss in that model.
Do not interpret phase averages as proof of sustained 100 Mbps in every window,
or as live Android/Rambler results. Final evidence retains every target flag.

For field testing, enable diagnostics before connecting, test MULTI without a
preceding SINGLE run, then SINGLE, another MULTI, and manual VPN reconnect.
Preserve the app/server exports if a phase fails. Observe idle RTT and uploads
as well as download speed. No additional mailbox purge is part of this test.

## Final exact-source verification

Source `b6d53de7ec97bef2d90582186a8398e409eed283` passes the [matched build and acceptance gates](https://github.com/Septonious/itom/actions/runs/42016829274) and [independent source checks](https://github.com/Septonious/itom/actions/runs/42016829270). Normal/race application, native and SDK tests, the IMAP fork checks and vet pass. Android passes 93 domain tests, 8 app tests and lint with zero errors. Both JNI libraries, server binaries and source archive are verified. The persistent Android signer is unchanged.

Six actual-server startup/reconnect cycles pass: three at 30/30 roles and three at 45/45, including server restarts and verified manual-stop local-capacity resets. The RC9 idle feedback is reproduced against frozen source; pressure clearing, delayed-update ordering, asymmetric full ledgers, active historical deferral and automatic idle cleanup pass.

The following are controlled TLS IMAP/HTTPS models, not live-provider results.

| Model / streams / cycle | Upload Mbps | Download Mbps | Max idle/post ms | Max loaded ms |
| --- | ---: | ---: | ---: | ---: |
| ordinary / 1 / 1 | 225.3 | 280.5 | 118.2 | 631.4 |
| ordinary / 4 / 1 | 143.4 | 209.0 | 120.6 | 768.5 |
| ordinary / 4 / 2 | 192.7 | 309.0 | 120.6 | 768.5 |
| ordinary / 32 / 1 | 269.1 | 305.9 | 122.1 | 343.1 |
| retained control / 4 / 1 | 284.4 | 320.5 | 108.5 | 305.0 |
| retained control / 4 / 2 | 218.6 | 329.0 | 108.5 | 305.0 |
| 40 old folders + metadata cost / 4 / 1 | 269.0 | 165.3 | 205.9 | 466.9 |
| 40 old folders + metadata cost / 4 / 2 | 192.5 | 193.4 | 205.9 | 466.9 |
| 40 old folders + metadata cost / 4 / 3 | 114.6 | 296.3 | 205.9 | 466.9 |

The 7 Mbps shared-link cancellation/recovery model completes at 4.72/4.46 Mbps upload/download, with maximum idle/post RTT 87.9 ms and loaded RTT 1119.6 ms. No account fails or is rejected.

The account-interference gate completes three full speedtests above 100 Mbps phase averages, with no active historical LIST. Full-phase capable-path and idle/loaded RTT gates pass. Strict pause/rolling-throughput misses remain in the raw evidence and metadata; all sustained-window and live-provider goals are not claimed complete.
