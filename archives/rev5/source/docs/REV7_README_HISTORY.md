# ITOM-0.9.1-Rev7 — test release

Rev7 starts from the released Rev6. It uses fresh unloaded per-account RTT for short-path selection, retains signed unread-byte placement costs after APPEND, keeps a responsive reserve stable during bulk publication, and gives short replies a bounded preference for the incoming physical path. The initial receive WINDOW follows the proven OPEN/OPEN_OK path when control capacity is safe. Capacity discovery follows fresh useful ACK turns rather than a fixed one-second pause between every growth step. Physical grants, uncertainty accounting, cleanup proofs, wire authentication, per-stream limits and Android network-switch shutdown settings remain enforced.

This is a test release. Actual TLS/IMAP, journals, supervisors and application sockets run in the Go fixtures; provider/access conditions are synthetic. The tests reproduce Speedtest-like TCP/HTTPS concurrency, not Ookla's private protocol. A passing model does not promise the same bandwidth on your phone/provider. See the included investigation and validation logs for final results, failed intermediate attempts and limits. No throughput, startup or probe deadline was relaxed.

## Install

Update the server from the release ZIP:

```bash
unzip ITOM-0.9.1-Rev7.zip
cd ITOM-0.9.1-Rev7
chmod +x ITOM-0.9.1-Rev7-linux-amd64
./ITOM-0.9.1-Rev7-linux-amd64 version
sudo ./ITOM-0.9.1-Rev7-linux-amd64 install
sudo systemctl restart itom
sudo itom version
```

Use the `linux-arm64` binary instead on an ARM VPS. The installed version must identify as `transport-rework1-rev7`. Keep server transport state; do not delete journals to upgrade.

The APK is a **separate test app**, `org.itom.client.dev.rev7`, labelled **ITOM Rev7**. The previous Android signing key was lost when the execution workspace reset. This package preserves your installed Rev6 and its state and avoids an incompatible update/reinstallation. Stop the old VPN, install `ITOM-0.9.1-Rev7-debug.apk`, import your original complete profile and apply your network preferences. An export that omits secrets will require credentials again; use your original profile/QR. Only run one ITOM VPN at a time. The version is `0.9.1-Rev7-debug`, code 9086. Native transport and server must both identify as `transport-rework1-rev7`.

## Build

Python 3.10+, JDK 17; Go 1.27.1 and Android dependencies are pinned. In PowerShell:

```powershell
python .\ITOM-0.9.1-Rev7-sources.zip all --expect-revision transport-rework1-rev7 --accept-android-licenses
```

The source builder exports the APK and amd64/arm64 server binaries into `dist` only after required checks pass. Keep the generated Android test signing key for future compatible Rev7 updates. It is separate from the distributable archive and sources.

## Go load and recovery checks

From the extracted `itom` source directory, with Go 1.27.1 in PATH:

```bash
python3 tools/run_speedtest_gate.py --suite full --log-dir speedtest-go-logs
python3 tools/run_speedtest_gate.py --suite heterogeneous --log-dir uneven-account-logs
cd core
ITOM_CONSECUTIVE_MOBILE=1 go test ./tunnel -run '^TestConsecutiveConstrainedLoadsAndIdleDATARecovery$' -count=1 -v
```

The serial full suite runs cold launches at 50/300 ms added access RTT, nine 200 Mbps HTTPS download/upload/duplex phases, uneven-account startup with 100 ms access RTT, two paced MTU-fragment 24-upload/24-download rounds, and three sustained 24-upload/24-download rounds with loaded echo probes. Each startup has 1, 8, 24 and 24 simultaneous TCP and HTTPS connections and five short verified request/reply turns per raw TCP connection. The uneven-account backend adds distinct service delays of 180, 240, 40 and 90 ms on four accounts, shared by their sockets and both endpoints. These are explicit stress assumptions, not inferred provider service times.

The 200 Mbps phases use 16 streams per direction, 30 measured seconds and 10 seconds warmup. Required active average and final-ten-second throughput remains >=50 Mbps, loaded probes <1000 ms, every application's first useful payload within 5 s, zero errors and preserved carriers. The normal sustained 48-flow gate uses its own synthetic 600 Mbps relay and 65 ms service model; its result is distinct from 200 Mbps qualification. Do not overlap performance runs with compilation or race instrumentation. `ITOM_LOAD_STACKS=1` enables failure goroutine captures for diagnosis; leave it off while measuring performance.

## Rev7 scheduling changes

Placement accounts for logical bytes still awaiting authenticated delivery, separately from APPEND completion. Idle calibration guides responsive reader selection; loaded-only samples use the unloaded pool estimate. Short replies and initial receive credit prefer the proven OPEN/data path. Two quiet paths stay stable during outstanding bulk. Continuous small TCP fragments enter bulk even when each fragment drains, while tiny request/reply messages and later idle exchanges retain their reserve. Useful ACK cadence governs bounded capacity probes.

The reader widens verified small-message backlogs up to the existing 16-UID limit. Unknown/large hot bodies keep four-UID turns. Ready control bursts drain before their coalescing timer expires. IDLE DONE propagation overlaps the following FETCH, with tagged completion joined before results and cancellation retained for the lane lifetime. Same-mailbox SELECT renewal also overlaps a bounded UID FETCH; unchanged UIDVALIDITY is confirmed before streaming, and changed/failed selection cannot admit old rows. Fresh parallel fast traffic briefly keeps sibling readers active; a single RPC or stale debt cannot sustain pool-wide polling. Quota accounting, unknown APPEND debt, wire framing and durable cleanup proofs remain unchanged.
