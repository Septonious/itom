# RC5 IMAP session-budget investigation (2026-10-17)

**Result: hypothesis rejected. Do NOT lower account_sessions to 5.**

The 10-session transport resource is already a **provider-wide per-account budget**, not 10 sessions independently on each endpoint. In `core/servercfg/transport.go`, `EndpointAccountSessions(server)` returns `AccountSessions/2` for the server and `AccountSessions/(2*Participants)` per client. With one enabled user, the configured default `AccountSessions=10` yields 5 server plus 5 client sessions, never 20. Existing unit test `TestTransportBudgetsPartitionBothEndpointsAndUsers` verifies this invariant. Client and server supervisors both construct their connection admissions using the endpoint-partitioned budget.

A proposed reduction to `AccountSessions=5` was tested in an isolated local copy using Go 1.27.1, 10 account identities, 24 application streams per direction, 60-second simultaneous measurement, modeled provider command delay 80ms, provider network 600Mbps, **provider aggregate IMAP per-account LOGIN cap 10**, 10-second warmup, and GOMAXPROCS=4. The result:

- Upload **14.143 Mbps**, download **41.117 Mbps** over full 60 seconds.
- RTT median **539ms**, p95 **959ms**, maximum **1011ms** (zero probe timeouts or misses, but latency ceiling FAIL).
- Physical total budget 5 partitions to 2 server + 2 client, leaving an unusable spare instead of preserving the normal 5+5. Increasing worker queues or reallocating DATA wallet credits does not restore the missing physical roles.
- Strict sustained 100 Mbps in both directions and <750ms RTT: **FAIL**.

This test used the stock 12MiB client/server physical journals and no production changes. It was conducted on the prior source snapshot with an identical session-budget implementation; the current RC5 source still has the same partition. It **does not** qualify the high-journal 24MiB tests or confirm that 5+5 connections are always fully allocated: actual simultaneous occupied socket counts may be lower and require separate proof.

**Decision:** Keep the default provider per-account budget 10 with 5/5 partition, preserve optional Login recovery, and do not commit any static reduction of the session limit. The remaining investigation is authenticated downstream/upstream delivery and credit feedback under large journal pressure; physical session allocation is not an identified root cause of the 100/100 deficit.

No release, APK/server binary, provider migration, account reset, wallet forgiveness, or physical quota change.
