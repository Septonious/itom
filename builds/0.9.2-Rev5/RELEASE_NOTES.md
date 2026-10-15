# ITOM 0.9.2 Rev5

Rev5 repairs the retained 180/192-message grant deadlock, bounds continuously paced BODY commands and unread downstream queues, grows envelopes from authenticated peer-delivery evidence, and restores DATA writers during account recovery. Existing physical debt and cleanup proofs remain authoritative. No profile, mailbox, wallet or journal reset is required.

Server manager **17. Server message** now supports a global message and per-client overrides. Individual messages override the global text; saving or clearing a global message clears all individual entries. Changes persist and reach connected clients without restarting transport. The Android message card supports Minecraft-style colors (`&0`–`&f`), bold (`&l`), italic (`&o`), underline (`&n`), strikethrough (`&m`) and reset (`&r`). Use `&&` for a literal ampersand. `&cHello &aWorld` displays red “Hello” and green “World”.

```bash
itom message --text "&eGlobal message"
itom message --uid CLIENT_UID --text "&cHello &aWorld"
itom message --uid CLIENT_UID --clear
```

The Android UI has darker surfaces, brighter text, stronger boundaries and vivid accents. **Settings → Server admin** appears for an enabled server-side client named `admin`, authenticated by its key. It provides the reversible server-manager controls, clients/accounts/invitations, live traffic/logs/trace, diagnostics export and message previews over encrypted IMAP. Ordinary applications using the admin VPN cannot sign management commands.

Each mutation first arms an independent durable watchdog. **60 seconds without verified two-way recovery reverts it; 60 continuous healthy seconds commits it.** Keep ITOM connected and wait for the trial before another mutation or a server-executable replacement. Stopping/disabling the current admin connection can intentionally trigger recovery. Permanent purges, uninstall and journal migrations stay CLI-only. See `docs/rev5/ADMIN.md` in the source for setup and the full control mapping. Internal logic, real TLS/IMAP transport and race tests passed; an actual systemd host and physical phone remain field-validation requirements.

All ten internal gates passed on source `e2c7e85f63007b301ab020ed6fa9b39676db49e0d23f75259384c1b149b28fc0`:

- 895 core test events; IMAP-fork tests and vet passed.
- Four complete 32-case field-suite repetitions, including one with 12 ms added durable-commit delay and real fsync. Cases include retained quota, thirty-second blackout, HTTP phases, repeated connection ramps, canceled downloads, chat under load and per-client notice delivery.
- Accounting/admin, recovery and native IMAP-admin reconnect race checks passed.
- 81 Android domain tests and 7 app tests passed; lint has 0 errors and 0 warnings; native bridge checks and both Android ABIs passed.
- Three SINGLE and three 24-stream MULTI runs passed independently, with fifteen accounts and physical UID history. Both directions must reach at least 90 Mbit/s over the full interval, final thirty seconds and final ten seconds. Every loaded RTT is below 1,100 ms; all thirty probes overlap useful traffic, with no misses or timeouts.

| Run | Lowest verified useful Mbit/s | Maximum loaded RTT ms | Duplex probes |
| --- | ---: | ---: | ---: |
| single-1 | 96.31 | 493.5 | 30/30 |
| single-2 | 96.99 | 622.3 | 30/30 |
| single-3 | 93.27 | 573.5 | 30/30 |
| multi-1 | 104.55 | 440.6 | 30/30 |
| multi-2 | 100.37 | 432.2 | 30/30 |
| multi-3 | 100.09 | 384.3 | 30/30 |

These are laboratory results, not a live-provider bandwidth guarantee. The user confirmed that resetting the router removed the earlier Wi-Fi bottleneck. The later Rev4 capture still contains target connection failures; the precise regular Speedtest-app error and previously reported whole-app mobile crash remain unconfirmed without matching server/fatal diagnostics. This release is a prerelease for physical-phone and live-provider validation.

Update **both** the Android app and server to Rev5. Install `ITOM-0.9.2-Rev5-debug.apk` over the existing app: package `org.itom.client.dev.rev7`, version code 9205, same signing certificate as the published Rev3/Rev4 testing APKs. `SIGNER.txt` records the verified certificate. Preserve existing state. Configurations using individual messages require Rev5 or later (server schema 5).

Choose `ITOM-0.9.2-Rev5-linux-amd64.bin` for x86-64 Linux or `ITOM-0.9.2-Rev5-linux-arm64.bin` for ARM64 Linux. Restore executable permission with `chmod +x` if the download tool drops it. `version` reports `transport-rework3-rev5`. Verify downloads against `SHA256SUMS`.

The source ZIP includes build tools and tests. `REV5_ANALYSIS.md`, `VALIDATION.json` and `FIELD_CORRELATION.json` contain the analysis and qualification evidence. GitHub Actions assembles the hashed assets, verifies their identity and all gate evidence, then verifies every uploaded size and SHA-256 before publishing. Private captures, credentials, caches and the signing key are excluded.
