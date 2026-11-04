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

## Verified release results

Source `66625b9c3dd40ff7ed60e338bfcae72ee4e9f8e6` is frozen.
[Matched build, safety, field comparison and throughput](https://github.com/Septonious/itom/actions/runs/41927771397);
[independent source acceptance](https://github.com/Septonious/itom/actions/runs/41927771423).
The complete local normal/race/native/SDK/vet gate also passed. The actual
downloaded amd64 binary completed three native startup/reconnect cycles at
30/30 roles and three at 45/45, including two server restarts and verified manual
stop counter resets. Android passed 93 domain tests, 8 app tests and lint with
zero errors. Both JNI hashes/revisions, both server hashes, source archive and
the existing signing certificate are verified. Android version code is 10133.

The paired verified-HTTPS replay applies the same 200 ms DATA APPEND service
cost to frozen RC7 and RC8, independently of their fast control path:

| Model | Upload Mbps | Download Mbps | Max idle/post RTT ms | Max loaded RTT ms |
| --- | ---: | ---: | ---: | ---: |
| RC7 | 5.74 | 7.49 | 93.5 | 769.2 |
| RC8 | 22.81 | 49.40 | 85.9 | 789.5 |

This is a 3.98× upload improvement in the replay. It is not a prediction of
Rambler bandwidth. The field screenshots remain the observed live baseline:
4.17/7.59 Mbps upload, 38.6/77.2 Mbps download and 198/173 ms baseline RTT.

Ordinary variable-propagation, high-rate verified-HTTPS results:

| Streams / cycle | Upload Mbps | Download Mbps | Max idle/post RTT ms | Max loaded RTT ms |
| --- | ---: | ---: | ---: | ---: |
| 1 / 1 | 171.2 | 212.9 | 88.8 | 266.4 |
| 4 / 1 | 179.7 | 208.5 | 102.9 | 298.9 |
| 4 / 2 | 108.6 | 227.4 | 102.9 | 298.9 |
| 32 / 1 | 260.6 | 310.4 | 92.8 | 266.1 |

The 7 Mbps shared-wire case completes at 4.71 Mbps
upload and 4.47 Mbps download, including download
cancellation and fresh HTTPS recovery. Maximum idle/post RTT is
122.4 ms; maximum loaded RTT is
1574.8 ms. All accounts and the session remain alive.

Complete-transfer 100 Mbps (where the wire permits), idle/light 300 ms and loaded
2000 ms gates pass for all final hosted cases. Strict half-second-pause and
rolling-five-second-throughput flags still miss in the 1- and 4-stream cases;
all flags and raw results remain in the evidence. Earlier local weak-link
attempts peaked at 2092/2019 ms and are preserved, not counted as passing the
loaded-delay gate. An initial local dependency download failed; the final full
local gate passed after seeding the verified existing module cache.

No real-device or live-provider qualification is claimed for RC8. Field-test a
MULTI run immediately after connecting, followed by SINGLE, another MULTI and a
manual reconnect. Enable diagnostics before these tests and export both endpoint
captures afterward. No further manual account cleaning is part of this update.
