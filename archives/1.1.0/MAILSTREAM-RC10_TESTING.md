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
