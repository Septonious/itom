# ITOM 0.9.2-Rev3

Rev3 addresses MULTI stalls, stream scheduling and feedback balancing after the Rev2 field captures. It preserves unspent authenticated repair permits across pre-APPEND deferrals, bounds speculative bulk work, interleaves stream prefixes, separates independent DATA occupancy from control routing, and distributes paired stream feedback across responsive accounts. The configured account limit remains lifted.

Local results describe declared laboratory cases. Real Android/provider sustained 100/100 Mbps with loaded RTT below 1000 ms remains a field qualification requirement. See [the implemented changes](docs/rev3/REV3_TRANSPORT_REWORK.md), [findings and measured results](docs/rev3/REV3_FIELD_FINDINGS_AND_RESULTS.md), and [build/install instructions](docs/rev3/REV3_BUILD_AND_INSTALL.md).

## Transport

All accounts retain equal roles. A single logical stream engine shares bounded memory and credit across accounts. Authenticated self-describing ITE2 DATA envelopes remove the normal C2S dependency on publishing a separate usage descriptor before DATA can be processed. Durable spend, grant scope, unknown-outcome debt and proof-based cleanup remain authoritative.

By default, each account/user has the existing C2S/S2C control folders and one additional DATA folder per direction. Independent DATA service is negotiated only when the actual connection is available. Provider limitations use the two-folder fallback. Explicit split mode requires the additional service. Optional two/four static DATA shards are supported; they do not increase mailbox quota. Control, small DATA, bulk DATA and cleanup have bounded scheduling resources. DATA reader repair preserves the logical session and application TCP sockets.

The former maximum of ten configured server accounts and the Android account-count ceiling are removed. Profiles/configurations are bounded by a 1 MiB serialized size, while active sockets, logins and memory have separate limits. Account-scale tests cover up to 128 accounts; this is a tested range, not a new fixed configuration ceiling. A large profile does not open unlimited provider connections.

Optional server `transport` configuration (exported to Android profiles):

```json
{
  "layout": "auto",
  "data_shards": 1,
  "account_sessions": 10,
  "endpoint_connections": 128,
  "login_parallelism": 4
}
```

`account_sessions` is the total provider-session budget for both endpoints of one account, not the number of configured accounts. Endpoint reservations and enabled-user participation divide it. Multiple users may require a larger provider budget or mixed mode. Folder/shard creation is additive; journals preserve their installed capacity and identity. DATA delivery objectives stay fixed at 800 ms (bulk) and 650 ms (fast); observing a slow path does not relax them.

Use both Rev3 endpoints for the new measurements. ITH10/ITP10/ITC10 peers intentionally reject incompatible Rev10 transport handshakes. See [upgrade and local APK instructions](docs/rev3/REV3_BUILD_AND_INSTALL.md) before installation. Preserve server quota journals, wallet state and mailboxes. Back up Android profiles before a clean reinstall.

## Local build and qualification

Python 3.10+ is required. The executable source ZIP verifies its SHA-256 manifest, extracts a fresh source tree and runs the pinned build tooling.

```bash
python ITOM-0.9.2-Rev3-sources.zip server --expect-revision transport-rework3-rev3
python ITOM-0.9.2-Rev3-sources.zip android --expect-revision transport-rework3-rev3 --accept-android-licenses
```

The full bundle includes the locally signed debug APK. The user authorized a clean local reinstall; the original Rev10 key is not required for that installation. Application ID remains `org.itom.client.dev.rev7`, version code is 9203, and debug version name is `0.9.2-Rev3-debug`. Preserve server journals and wallet state.

From the extracted `itom` directory, run performance measurements serially with Go 1.27.1, without builds or race instrumentation in parallel:

```bash
python tools/transport_qualification.py --go /path/to/go --streams both --accounts 10 --seconds 60 --warmup 10 --repeats 3
```

The new runner requires fresh endpoint state, the named test to execute, unchanged source fingerprints, useful application bytes in both directions, sustained and final-tail throughput of at least 100 Mbps, every loaded probe below 1000 ms, and no missed or timed-out probes. Failure, missing/empty results and a skipped required test cannot pass. Results are explicitly LABORATORY evidence. Provider-backed Android field trials and Speedtest SINGLE/MULTI remain necessary; a passing Go test is never a real-world capacity claim.

The full design comparison and proposed field acceptance matrix are in [the rework plan](docs/rev3/REV2_TRANSPORT_REWORK.md). The final delivery report records actual executed checks and remaining qualification work. Historical Rev10 documentation is retained under `docs/reference/` as reference, not Rev3 installation instructions.
