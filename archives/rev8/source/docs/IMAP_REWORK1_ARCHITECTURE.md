# IMAP REWORK1 architecture and compatibility

Status: development source candidate. Local correctness is distinct from the current 50 Mbps acceptance and real-provider/Android qualification. Read the packaged handoff's test summary for actual outcomes.

## Delivery policy

`deliveryPolicy` replaces capacityProbe, fastRamp and bulkRecovery and their mutually competing decisions. The state machine is IDLE / NORMAL / PROBE_UP / CONGESTED / OUTAGE / RECOVERY. New admission ranges remain owned by `deliveryController`; acknowledgements release only logical flight. Durable journal/wallet debt remains independent and unchanged.

Default bounds: 1 MiB initial, 256 KiB minimum, 16 MiB maximum bulk flight, 128 KiB independent interactive reserve, 250 ms decisions, 800 ms bulk delivery-age target, 650 ms fast-delivery feedback ceiling. The age objective cannot increase with a learned floor. A slow bulk ACK alone is not proof of application congestion when fresh interactive progress contradicts it. Useful progress, demand classification, fresh interactive age and no-progress age determine whether to grow, hold, throttle or recover. Plateau probing has a one-second cooldown; recovery requires healthy observations. Idle clears stale rate estimation without refunding retained bytes.

These are feedback signals, not a mathematical guarantee that application RTT always stays below one second. The application acceptance gate measures actual request/response RTT independently, including missing probes and errors. No privileged PING is substituted for that measurement. The 16 MiB ceiling permits exploration; it does not establish achievement of any throughput target.

## Account I/O and placement

`accountWorkBudget` is shared across account DATA APPEND, urgent control, receive and maintenance operations. Class ceilings are 2/2/2/1 for DATA/urgent/receive/maintenance, with seven total operation slots. Each queued class gets its first protected slot before another class receives an additional slot; FIFO ordering within a class and DATA worker MIME-byte DRR across users remain. Provider connection fallback still controls actual live sockets. This budget is per account within each endpoint process, not a distributed provider-wide rate limit.

Client APPEND and server shared DATA workers use the same admission authority. Cancellation and completion return a lease once. Measured service cost, writer occupancy, unread physical DATA and control debt inform stable rotated account ranking. Grants and physical slots still provide the authoritative publication reservation; an estimated score cannot authorize an APPEND or quota refund.

`capacityNotification` broadcasts generation changes to every waiting predicate. Waiters capture a generation before checking capacity. This avoids stranding other ready senders after a one-token wake is consumed. Existing channel notifications remain for compatibility with other internal paths.

A fair session-wide preparation queue now ties logical owner work to live physical bulk slots. Owner shares adapt to the number of active logical owners; one owner can use up to eight preparations while many owners share the available slots. FIFO selection skips only owners already at their share. Preparation occurs before sealing, grants, journal writes and APPEND; cancellation releases it once. Idle collectors hold no permit, and the fast worker bypasses this budget. Absolute batch queue deadlines and protocol batch limits are preserved. An isolated extension of assembly deadlines showed no decisive gain and was removed.

## Control and receive behavior

Evidence-identical STATUS/NACK/STATUS_REQUEST repetition is bounded to one emission per 250 ms per lane/key. Different state is never conflated. This does not suppress GRANT_USAGE, publication descriptors, durable receipts, authentication, OPEN or PING. The dedup map is bounded to 256 keys and reports suppressed advisory records. Existing cumulative WINDOW/RECEIPT coalescing, sealed Noise counters and physical receipt/GC proof semantics are preserved.

Quiet IDLE audits are bounded to 300 ms and active audits to 150 ms; a recent response hint gets a 150 ms audit. Handshake/selection proof validation remains on its independent two-second cadence, avoiding a SELECT flood. Request-like wakes and reply isolation remain, as do quiet reader reserves. Receive turns adapt from four to sixteen UID numbers when complete tails saturate, while hot/slow/empty turns remain small; streamed BODYs are emitted without waiting for tagged completion. UIDVALIDITY, allocation bounds, tail-gap revisits and immutable inventory proof are unchanged. Losing a notification must not create a two-second quiet receive penalty, and an idle readiness audit must not become a repeated authentication/SELECT operation.

## Application measurement and tests

Verified application consumption timestamps bytes into immutable-duration buckets. Warm-up and late bytes never count; interval totals and the final tail are independent of sampling scheduler lag and probe completion. A final probe may complete after the byte window without extending goodput. Corruption, missing windows, duplicate windows, early EOF, absent overlap and missing/timeout probes remain failures.

The current gate requires at least 50 Mbps useful delivery in each active direction over both the complete 30-second interval and its final ten seconds, every application RTT strictly below 1000 ms, and no missing probes or integrity failures. Established echo/TLS connections, logical identity and carrier epochs are checked after stress and an idle gap.

The normal speed gate uses StartClient/Serve supervisors and Client.Open with 24 uploads +24 downloads, 10 accounts and three measurements on the same sockets/session. The old raw-lane 130 Mbps scenario remains an explicit comparison measurement, not a qualifying result. HTTPS qualification covers sequential download/upload/duplex phases, public supervisors and persistent transport state. Each request captures a unique run identity and immutable byte window; delayed bytes and cancellation from an earlier upload cannot contaminate a later run of the same phase. Native/device/TUN and real-provider claims require independent testing.

`tools/rework_gate.py` records exact Go version, commands, exit status, named pass/fail/skip outcomes and a runtime-source fingerprint. It rejects zero-test selections, skipped required tests and changed runtime sources. `local`, `compare130`, `performance` and `http` are distinct suites. Local PASS cannot certify a performance FAIL.

## Preserved compatibility

ITH9 negotiation, ITD3 batching, authenticated grant/owner/session/generation bindings, Noise counters, replay checks, canonical MIME verification, UID/deletion proof, on-disk journals and wallet formats are unchanged. No journal migration or destructive repair is introduced. Existing security, corruption, crash-recovery, MIME-rewrite, grant-accounting and stream-lifecycle tests are retained. Tests specific to the removed competing probe/ramp/recovery algorithms are replaced by unified-policy, broadcast, admission, suppression and consumption-clock invariants.

Multi-owner physical mux, DATA/control piggyback framing, SACK and a distributed provider-wide scheduler are not implemented. They require a separate authenticated protocol/proof design and negotiation; they must not be inferred from account ranking or the new scheduler. The original 100+ Mbps target remains aspirational; if that gate remains unmet, these mechanisms and durable GC/control costs are concrete remaining investigation areas rather than grounds to declare success.

## Identity and release

This is one coordinated candidate with revision `transport-rework1-field79`, version `0.10.0-dev1`, APK metadata versionCode9079. FIELD79 is a unique artifact identity, not production qualification or a sequence of released experiments. FIELD77 is the supplied baseline. No VPS was updated, no APK/device qualification was performed, and no new wire/database schema was activated.
