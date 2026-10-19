# ITOM 1.0.0 RC10 — development candidate

RC10 starts from tagged RC2 and keeps separately reviewed corrections. Production transport windows, framing, polling and recovery policy remain RC2. Exact journal scan allocation reduces empty/sparse metadata allocation without changing durable quota or proof rules. Development and qualification are still in progress; this source is not a stable-release certification.

The current reference assumes a healthy server with a 20 ms route to Rambler. Client paths use 40 ms normally, 20/50 ms boundary cases and a 100 ms sensitivity case. See [the test model and its limits](docs/rc10/HEALTHY_SERVER_REFERENCE.md) and [handoff](HANDOFF.md). Older field investigations describe their own versions.

## Identity and upgrades

Runtime revision: `itom-1-0-0-rc10`. Android application ID: `org.itom.client`; version code `10009`, version name `1.0.0-rc10`. Update both endpoints together. Preserve accounts, profiles, quota journals, wallet state and logical identities. Check the APK signing certificate against the installed app before an in-place update; this source does not establish access to the previous signing key. Reinstallation can lose private profiles and wallets.

## Verify and build

Use Python 3.10+ and the bundled pinned tooling. Run from this source directory:

```bash
python3 tools/build.py check --expect-revision itom-1-0-0-rc10
python3 tools/transport_qualification.py --go /path/to/go --profile healthy-server --client-rtt-ms 40 --server-rtt-ms 20 --minimum-mbps 100 --repeats 2
```

The CLI qualification target is strict 100 Mbps in both directions and tails, with zero speed margin. The existing CI minimum remains 75 Mbps and max loaded RTT 750 ms; no existing threshold was relaxed. Every scheduled probe, positive useful progress in every one-second window, original sockets and post-load integrity remain required. A deliberately 10/20 Mbps client profile checks continuity/latency and cannot certify 75/100 Mbps throughput.

After all required checks pass, build the paired artifacts:

```bash
python3 tools/build.py server --expect-revision itom-1-0-0-rc10
python3 tools/build.py android --expect-revision itom-1-0-0-rc10 --accept-android-licenses
```

These builds verify source/runtime/native/APK identities and include their own checks. Successful compilation alone does not certify transport performance. The qualification workflow packages only after all its required gates pass. A separate explicitly requested field-test workflow builds after source, safety, Android tests and lint, and labels unresolved performance qualification in TESTING_NOTES.txt. Real-device/provider measurements remain separate from synthetic qualification.

## Server and admin

The binary opens the manager menu without arguments. `version` prints its revision; `serve --state /var/lib/itom` runs the configured server. Persistent IMAP connections and hostname-verified IP pinning are retained; exported profiles carry configured pins.

The app's authenticated admin UI retains categorized server management and a 60-second rollback for reversible actions. Destructive mailbox purges, uninstall and full account reset remain CLI-only. Individual client messages override the global message; updating the global message clears individual overrides. Minecraft-style color codes are supported. The dark UI and single diagnostics export progress indicator remain selected.

Configuration, journals, keys and private diagnostic captures are not project-release assets. Historical evidence remains available in the repository archives; it is not evidence that the current candidate passed.

## Requested RC10 field-test package

The user explicitly requested an APK and server binaries for testing after reviewing the paired RC2 comparison. This build retains the RC2 controller parameters and removes allocations from bounded latency-feedback quartiles using stack copies. Sample selection, caps and outputs are unchanged; equivalence and zero-allocation checks pass. This is a small CPU/GC improvement, not proof that RTT spikes are fixed.

Prior paired normal load cases passed for both RC2 and RC10, while both failed low-capacity ramps and the100ms-client upload/tail floor. Those failures remain open and thresholds remain unchanged. A testing package is not a qualified stable release. The package includes exact source, Linuxamd64/arm64 binaries, an Androiddebug APK with both ARM ABIs, signer metadata and checksums. Installed-certificate compatibility has not been established; preserve all private state.
