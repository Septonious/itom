# ITOM-0.9.1-Rev10 — all-account recovery and larger home control

Revision: `transport-rework2-rev10`. Baseline: the exact released Rev9 sources.

Rev10 fixes the all-account startup stall reproduced from the supplied Rev9 field traces. Accounts with retained physical reservations can now receive smaller fast and bulk DATA grants from their genuinely free capacity. Historical debt, local reserves and cleanup proofs remain enforced. The matched ten-account TLS IMAP fixture now brings up all ten accounts and twenty directions, with TCP echo traffic on each account. Windows host-test repair, live announcements and the English interface from Rev9 are retained.

The transport combines cumulative receive acknowledgements and credit into an authenticated ITH9 STATUS snapshot. Reordered snapshots merge monotonically at the pump, carrier queue and physical batch layers. Bulk feedback keeps a stable path outside the short-traffic reserve when an ordinary healthy path is available. Hostname dialing normalizes IPv4-mapped resolver results before the existing address-policy check. Physical debt, cleanup proofs, authentication and performance thresholds remain enforced.

The Android interface is entirely English. Home has a compact scrollable server-message field to the right of ITOM in the header and restores the large connection control, without the former subtitle or top-right shortcuts. Settings has four entries: VPN networks, SOCKS5, Notifications and Diagnostics. Switches use mint for on and violet for off. Enabling SOCKS authentication generates any missing credentials; existing credentials are preserved. Diagnostics shows current per-account metrics and offers only the full ZIP export.

The Rev8 manager features are retained: numbered client and account selections, removal of a client registration, per-client live useful TCP traffic and server uninstallation. Backup-account reservation is deferred.

This is a development release. Passing synthetic tests does not establish live-provider throughput, phone Speedtest Multi reliability or complete 200 Mbps qualification. See `docs/REV10_AUDIT_AND_NOTES.md` and the accompanying validation evidence. The archive includes a compiled APK, Linux amd64/arm64 binaries, complete sources and validation reports. Install the supplied APK directly; local compilation is optional.

## Optional local rebuild

Python 3.10+ is required. From the folder containing the executable source archive:

```powershell
python .\ITOM-0.9.1-Rev10-sources.zip all --expect-revision transport-rework2-rev10 --accept-android-licenses
```

The launcher downloads pinned tools, verifies source integrity, runs mandatory Go/IMAP checks, builds both Linux architectures, and checks Android compilation, domain tests, lint, APK identity and native libraries. Verified outputs go into `dist` beside the ZIP:

- `ITOM-0.9.1-Rev10-linux-amd64`
- `ITOM-0.9.1-Rev10-linux-arm64`
- `ITOM-0.9.1-Rev10-debug.apk`

Use `server` instead of `all` to build only the server. Reuse your existing build cache and Android debug signing key when updating a locally built installation. The test application package remains `org.itom.client.dev.rev7`, with display name **ITOM** and version code 9089. The included APK is signed with this release's development key. It differs from the previously supplied Rev9 APK signing key; Android cannot install it over that APK in place. Preserve/export your profiles before changing installations, or rebuild these sources with your existing key. Do not reset server journals. No private signing key is distributed in this bundle.

## Update server

From the development bundle's `ITOM-0.9.1-Rev10` directory:

```bash
chmod +x ITOM-0.9.1-Rev10-linux-amd64
./ITOM-0.9.1-Rev10-linux-amd64 version
sudo ./ITOM-0.9.1-Rev10-linux-amd64 install
sudo systemctl restart itom
sudo itom version
```

Substitute `linux-arm64` on ARM. Both endpoints must identify as `transport-rework2-rev10` for comparisons. Keep existing server journals. Update the Android transport from this source archive too.

## Manager

Run `sudo itom`.

- **4. Users / clients:** add, rename, enable/disable or remove a selected client. Configuration changes require the service to be stopped using menu item 8, then started using item 7. Removal is confirmed by `yes`; it revokes that registration without deleting mail or physical quota journals. A new registration receives a new UID, key and folders. Retired IDs cannot be reused.
- **5. Export Android profile:** choose an enabled client by number. UID entry is optional legacy compatibility, not required. Names and UIDs appear together so duplicate names are distinguishable.
- **15. Live client traffic:** choose all clients or one client. Displays active TCP sockets, cumulative upload/download in decimal MB and rates in Mbps. Counters reset when the server restarts; they remain available with diagnostics off.
- **16. Remove ITOM from server:** confirms with `yes`, stops and disables the service, and removes the installed service file and binary. Configuration, keys, journals and mailbox contents remain available for reinstallation.

CLI traffic commands:

```bash
sudo itom traffic
sudo itom traffic --watch
sudo itom traffic --watch --uid 12345678
```

Client removal lazily upgrades server configuration to schema 3. Earlier Rev7 binaries cannot read that schema; do not downgrade after removing a client. Removed-client journal debt is retained for audit and is not automatically reclaimed by removing the registration.

## Server messages

Update both the server and Android app to Rev10. Rev10 requires the authenticated small-recovery-grant capability; mixed Rev9/Rev10 peers do not connect. Existing profiles and physical quota journals remain usable.

Run menu item **17. Server message**, or:

```bash
sudo itom message --text "Maintenance starts at 22:00 UTC."
sudo itom message
sudo itom message --clear
```

Use the same state directory as the service (pass `--state` when customized). A changed message is persisted immediately without restarting ITOM. The server sends the current revision to each logical client session, retries unacknowledged delivery at five-second intervals, and stops when acknowledged. Identical text is a no-op. Clients ignore stale revisions and display duplicates only once. Active sessions receive changes and clears. The home field resets when the session ends. Messages support up to 2,048 UTF-8 bytes, with newlines and tabs allowed.

Setting a message upgrades the configuration to schema 4. Do not use older management binaries afterward: they cannot safely read this schema.

## Account metrics

Upload/download are current rates of DATA payload carried by each account, sampled once per second; retransmitted DATA contributes to the carried rate. TCP streams count logical streams owned by that account, even if DATA temporarily uses another account. RTT is the latest transport estimate. IMAP latency is a moving average of successful fetch and append command durations; IDLE waits are excluded. Active lanes retain the existing ready transport-direction count, out of two per account. Metrics work with tracing disabled. No backup accounts or dedicated control-account allocation is added.

## Additional load qualification

From an extracted `itom` directory, with Go 1.27.1 in PATH:

```bash
python3 tools/run_speedtest_gate.py --suite full --log-dir speedtest-go-logs
```

Run performance suites serially without simultaneous builds or race instrumentation. The native TCP/IMAP integration fixture is enabled only for host tests with `-tags=itomtest`; the private-target fixture is absent from production binaries.
