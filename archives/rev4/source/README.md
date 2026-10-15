# ITOM 0.9.2-Rev4

Rev4 repairs reconnect and application-startup failures found while testing Rev3. It keeps the logical TCP actor alive across carrier replacement, answers OPEN on the logical lifetime, routes new OPEN through the selected carrier, starts DATA reception after activation/bootstrap, and uses one active DATA mailbox per actual receive socket. Delivery budgets use bounded authenticated path calibration rather than repeatedly reducing a healthy path below its measured delay.

Healthy TCP streams can advance a bounded charged frame beside delayed published flight. Short recovery uses authenticated NACKs with separate pacing, and the wallet accepts reordered unseen offers while retaining bounded retirement protection.

Use matching Rev4 endpoints. Preserve server configuration, quota journals and wallet state. The Android package remains `org.itom.client.dev.rev7`, version code 9204. The build reuses the Rev3 testing signing certificate so profiles and wallet history can remain installed. Check the published `APK_SIGNER.json` before updating another APK.

See [the field investigation](docs/rev4/REV4_FIELD_ANALYSIS.md), [the test design](docs/rev4/REV4_TEST_DESIGN.md) and [installation instructions](docs/rev4/REV4_BUILD_AND_INSTALL.md). Historical Rev2/Rev3 reports describe their own sources and builds.

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
python ITOM-0.9.2-Rev4-sources.zip server --expect-revision transport-rework3-rev4
python ITOM-0.9.2-Rev4-sources.zip android --expect-revision transport-rework3-rev4 --accept-android-licenses
```

The normal Go build executes the new field-failure application cases and mandatory provider-outage cases. Repeat the application suite serially with Go 1.27.1:

```bash
python tools/field_qualification.py --go /path/to/go --results work/field-results --repeats 3
python tools/field_qualification.py --go /path/to/go --results work/commit-delay-results --repeats 1 --commit-delay-ms 12
```

The second command retains real fsync and adds a declared server commit delay through the test-only build tag. Every required case must execute, preserve source identity and pass. Missing/skipped cases and failures remain failures. Raw user diagnostic captures and private signing keys are excluded from all source and release archives.

Release qualification also requires every fresh SINGLE/MULTI run to pass:

```bash
python tools/transport_qualification.py --go /path/to/go --streams both --accounts 15 --seconds 60 --warmup 10 --repeats 3
```

Laboratory tests do not certify the real phone/provider path. The release gate applies a 10% tolerance to the 100/100 Mbps and <1,000 ms loaded RTT objectives: both directions and the sustained tail must reach 90 Mbps, every loaded RTT must be <1,100 ms, and there must be no timed-out/missed probes. Each run must pass; averages across failed runs cannot qualify. Real-device SINGLE/MULTI and post-reconnect application checks remain independent.
