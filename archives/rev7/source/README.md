# ITOM 0.9.2-Rev7

Rev7 repairs physical control sockets without restarting encrypted carriers, prioritizes critical socket repair, retries missing authenticated handshake replies, bounds missed-IDLE audit delay, and reworks retained-state duplex admission and ordered multiplexed batching. See [the paired-capture investigation](docs/rev7/REV7_ANALYSIS_2026-10-16.md) and [exact qualification evidence](docs/rev7/evidence/rev7_validation.json). All internal gates are required before packaging.

Rev6 bounds optional startup sockets, keeps idle IMAP sockets alive without blocking active commands, adds a CLI-only full accounts reset, exposes safe LOGIN response categories and removes the settings introduction banner. See [startup and idle analysis](docs/rev6/REV6_ANALYSIS_2026-10-16.md).

Rev5 addresses the paired Wi-Fi/mobile field failures: recovery uses remaining ordinal capacity without dropping historical debt; paced DATA reads yield before long multi-BODY commands time out; downstream batch growth depends on authenticated BODY receipts rather than APPEND speed; provider mailbox-state setup errors reconnect with a bounded fresh retry. The mandatory application, recovery and RTT gates remain required; Rev7 explicitly waives the throughput floor at the user’s request. Real-provider speed is separate from laboratory qualification.

Healthy TCP streams can advance a bounded charged frame beside delayed published flight. Short recovery uses authenticated NACKs with separate pacing, and the wallet accepts reordered unseen offers while retaining bounded retirement protection.

Use matching Rev7 endpoints. Preserve server configuration, quota journals and wallet state. The Android package remains `org.itom.client.dev.rev7`, version code 9207. The build reuses the Rev3 testing signing certificate so profiles and wallet history can remain installed. Check the published `SIGNER.txt` before updating another APK.

See [the Rev5 investigation and qualification](docs/rev5/REV5_ANALYSIS_2026-10-15.md), [the earlier field investigation](docs/rev4/REV4_FIELD_ANALYSIS.md), [the test design](docs/rev4/REV4_TEST_DESIGN.md) and [installation instructions](docs/rev4/REV4_BUILD_AND_INSTALL.md). Historical Rev2/Rev3 reports describe their own sources and builds.

## Transport

One logical TCP engine shares bounded memory/credit across the authenticated account pool. Accounts keep equal roles and independent physical carrier lifetimes. Self-describing authenticated DATA envelopes, durable spend, unknown APPEND debt and observed-body/deletion proof remain authoritative. No reconnect or congestion decision forgives physical quota debt.

Each account/user uses control mailboxes plus one active DATA mailbox per direction when an independent receiver is available. Mixed fallback remains available under provider connection limits. Historical two/four-shard folders remain owned and inventoried/reclaimed. Multiple shards are not advertised for one selected receive socket; striping happens across actual independent account connections. DATA role repair preserves the logical TCP sockets.

Configured account-count ceilings remain removed. Active provider sockets, login concurrency, serialized profile size and session memory stay bounded. Example transport settings:

```json
{"layout":"auto","data_shards":1,"account_sessions":10,"endpoint_connections":128,"login_parallelism":4}
```

`account_sessions` budgets both endpoints per account. Multiple users share that budget. Folder additions preserve existing journal identity/capacity. The queue controller separates the measured path floor from extra delay, with hard delay/memory bounds and outage detection. Application RTT and useful throughput are checked separately; controller calibration never constitutes a speed qualification.

## Build and verify

Python 3.10+ is required. The executable source ZIP verifies its manifest and extracts a fresh tree before the pinned build:

```bash
python ITOM-0.9.2-Rev7-sources.zip server --expect-revision transport-rework3-rev7
python ITOM-0.9.2-Rev7-sources.zip android --expect-revision transport-rework3-rev7 --accept-android-licenses
```

The normal Go build executes the new field-failure application cases and mandatory provider-outage cases. Repeat the application suite serially with Go 1.27.1:

```bash
python tools/field_qualification.py --go /path/to/go --results work/field-results --repeats 1
python tools/field_qualification.py --go /path/to/go --results work/commit-delay-results --repeats 1 --commit-delay-ms 12
```

The second command retains real fsync and adds a declared server commit delay through the test-only build tag. Every required case must execute, preserve source identity and pass. Missing/skipped cases and failures remain failures. Raw user diagnostic captures and private signing keys are excluded from all source and release archives.

Rev7 stability qualification requires every fresh and retained-state SINGLE/MULTI run to pass:

```bash
python tools/transport_qualification.py --go /path/to/go --streams both --accounts 15 --seconds 60 --warmup 10 --repeats 1 --stability-only
python tools/transport_qualification.py --go /path/to/go --streams both --accounts 15 --seconds 60 --warmup 10 --repeats 1 --retained-state --stability-only
```

Laboratory tests do not certify the real phone/provider path. The user explicitly waived the 90 Mbps throughput floor for Rev7. Speeds remain recorded, but no speed threshold qualifies this release. Every one-second measured window must carry useful bytes in both directions, every loaded RTT must be <1,100 ms (the existing 10% tolerance), and all thirty loaded probes must overlap duplex traffic with no timeout or missed probe. Original TCP sockets, logical sessions and healthy carrier epochs must survive the load. Six same-socket exchanges after idle gaps, a fresh 256 KiB verified application transfer and another original-socket exchange must succeed after load. Each run must pass; averages across failed runs cannot qualify. Real-device SINGLE/MULTI and post-reconnect application checks remain independent. Omitting `--stability-only` runs the separate historical 90 Mbps performance policy.

## Server messages

Manager menu 17 edits the global message or selects a specific client. Individual messages override the global one. Saving or clearing the global message clears all individual entries. Removing an individual override restores the global message. Updates reach connected clients automatically and persist across server restarts.

```bash
itom message --text "&eGlobal message"
itom message --uid CLIENT_UID --text "&cHello &aWorld"
itom message --uid CLIENT_UID --clear
```

The Android home card supports Minecraft-style `&0`–`&f` colors, `&l` bold, `&o` italic, `&n` underline, `&m` strikethrough and `&r` reset. Use `&&` for a literal ampersand. Each color code resets prior styles. Messages allow up to 2,048 UTF-8 bytes including codes. Configurations using individual messages require Rev5 or later (schema 5).

## Android server administration

The darker Android interface includes **Settings → Server admin** for an enabled server-side client named `admin`, verified using that client's key. It provides accounts, clients, invitations, formatted messages, live traffic/logs/trace, diagnostics export, resource settings and reversible service controls over encrypted IMAP.

Every mutation arms an independent watchdog first. Sixty seconds without verified two-way recovery causes rollback; sixty continuous healthy seconds commits the change. Keep the VPN connected during the trial. Permanent purges, uninstall and journal migration stay CLI-only. See [admin controls and automatic recovery](docs/rev5/ADMIN.md) for setup, the complete control mapping and validation limits.

## Reset the accounts database

Stop the service, open server manager **3. Mail accounts → 6. Reset accounts database**, and type `RESET ACCOUNTS`. All active credentials and retired account definitions are removed. Server/client identities, provider messages and accounting files remain. A new account-ID namespace prevents old journal identities being assigned to new accounts. Re-add current provider credentials and export replacement profiles to **every client** before starting again. Reset configurations use server schema 6; older binaries cannot read them. The reset is CLI-only and is not a mailbox purge or forensic disk erase.
