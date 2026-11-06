# Accepted RC18 transport and RC19 diagnostics/polling audit

The user accepted RC18 as the first candidate reliably meeting the goals.
Both supplied speedtest screenshots describe ITOM: 109/131 and 143/144 Mbps,
153/161 ms idle RTT, 11/16 ms jitter. RC18's original build and tag stay frozen;
FIELD_BASELINE.json and the separate RC18 FIELD_ACCEPTANCE record document this
later field acceptance. The historical RC10 milestone is not rewritten.

The supplied Android/server captures show all ten accounts and thirty roles
healthy, zero DNS failures and zero DNS/TCP worker refusals. The Android capture
reports 432 DNS cache hits, 101 misses and three shared queries. Receipt RTT has
an observed minimum around 133 ms; direct IMAP command timing is a different
measurement. The 111 application open failures, with no open timeouts, are not
by themselves proof of a transport failure or a single remote failure cause.
Personal captures remain private; their hashes are in the acceptance record.

Missing rates have a concrete data-path cause: the service added deltas only to
its SessionMachine snapshot, while DiagnosticsScreen used the separate native
Diagnostics JSON. Account counters were present and increasing on all accounts.
RC19 computes bounded shared rate windows in the core; it exposes readiness of
the first sample and stable account IDs. Logging and UI observers cannot reset
one another's windows. The screen and export overlay the current session sample
including its native counters, independent of tracing, while preserving recorded
provider evidence. A stopped session keeps its saved report.

The per-account TCPStreams value means a current tunnel stream whose outgoing
packet was observed on that account. Bulk striping and interactive duplication
make these counts overlap; they include opening/closing tunnel streams. They
cannot be summed into a global established socket count or compared to three
IMAP roles. RC19 labels the association explicitly and exposes TransportStreams
separately. Both native public APIs decorate TCP with the established application
connection count, rather than mixing it with the core's tunnel-stream count.
The two counts need not equal even after adding opening sockets (direct policy
connections and closing streams have distinct lifecycles).

The selected control reader was already continuously polling forward UID FETCH
ranges, sleeping only one millisecond on an empty result. Its periodic NOOP
added a provider round trip before the next poll every two seconds. RC19 removes
that extra command and measures completed empty FETCH service instead. It keeps
streaming delivery before tagged completion, absolute forward ranges across UID
holes, bounded message parsing, cancellation, writer ownership and the idle
floor. DATA IDLE/repair and writer keepalives are not changed. A real streaming
IMAP regression crosses the old probe boundary with a late message and a UID
hole; the frozen RC18 reader reproduces the redundant command.

No startup allowance, resource cap, congestion controller, DNS policy, folder
layout or cleanup rule is changed. The original thirteen hosted jobs remain;
three predeclared matched pairs compare RC19 to the immutable accepted RC18.
All modeled RTT statistics and strict misses are retained, without imposing or
claiming a new percentage latency improvement or sub-100 ms field result.

UDP datagrams can be framed inside TCP, as VLESS does; this is explicitly
excluded from RC19 following the user's priority instruction. First finish
latency, speed and stability of the existing transport. No new feature is added.

The three predeclared constrained-transfer comparisons all completed. Their
geometric payload ratios were 1.01857 upload and 1.00122 download versus RC18.
Idle probe medians improved in every pair (approximately 10% combined). Loaded
RTT medians and p95 were mixed across pairs, so no general loaded-latency
improvement is claimed. The raw comparisons, every phase and all strict target
misses remain in the evidence. These are controlled probes, not phone gaming
RTT or Ookla jitter; sub-100 ms is still unverified.
