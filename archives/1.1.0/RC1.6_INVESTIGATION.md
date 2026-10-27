# RC1.6 connected-session investigation

The supplied RC1.5 client reaches READY in 4,455 ms, selects all 20 lines, and
reports three application TCP connections. Its authenticated request succeeds
using the 31-character representation. The attached server executable hashes to
the published RC1.5 amd64 artifact. An executable is not a server diagnostic
capture; the running server's queue and grant state are absent from these inputs.

The exported observation contains 6,950 uploaded and 8,517 downloaded application
bytes, followed by zero current throughput and no RTT. Therefore it does not
establish that absolutely no application bytes crossed the VPN. It also cannot
identify why those application connections failed to make useful progress.

Two confirmed omissions were found: the replacement client never populated
RTTMillis, and enabled client traces contained startup events only. While
connected, the Android export also selected the UI counters instead of the
richer native runtime diagnostic document.

Changes under validation:

* A private authenticated virtual echo exit and one bounded persistent probe
  stream measure the real nonce echo over ordinary accounted OPEN/DATA/receipt
  paths. Samples include real peer completion; timeouts and wrong echoes are
  not successful samples. No network latency is hardcoded or inferred from IMAP
  command timings. Probes do not control congestion, grant or cleanup policy.
* Enabled client tracing records bounded runtime/flow/receipt/publication state
  every two seconds and on close. Targets, payloads and credentials are excluded.
* The connected export includes native runtime state, application-open counters,
  DNS failures and the existing per-session DNS fallback latch.
* A new integration check launches the actual native client in a separate process
  against the production server API and TLS IMAP fixture. It sends real HTTPS
  through SOCKS and raw IPv4 packets through Attach and the gVisor TCP frontend,
  using ten accounts, 32-character provider keywords and 20/40/30 ms scripted
  command service. It restarts with original wallet paths and tests 4 KiB and
  128 KiB requests in both directions. No selected transport, keys or grants are
  injected into the native client. Unix datagrams substitute only for the
  platform TUN file descriptor; Android permission/network binding is not modeled.

The bridge checks pass with the previous RC1.5 traffic implementation. They do
not reproduce the reported field stall. Early fixture failures were incomplete
APPEND EOF assertions during intentional child shutdown; a child-owned marker
now distinguishes successful intentional close from ordinary traffic failure.
The application deadlines and fixture mailbox limits were not increased.

These changes fix missing measurement/evidence. They do not yet prove a fix for
the user's application-traffic stall, qualify 100 Mbps or establish a 300 ms
idle/light RTT bound. Matched client/server field diagnostics remain necessary.

Additional correction: client READY now waits for an actual authenticated DATA
nonce echo, not just permission. A negative fixture whose control path works
but whose accepted server DATA remains invisible reproduces false readiness
before this change. It now returns V2_STARTUP_PATH without emitting READY. This
fix prevents a false connected state; it is not a claim that missing field DATA
has been recovered.

Expanded local race testing also exposed an intermittent native TLS deadline
failure after earlier successful transfers. A subsequent three-run cohort and
profiled run passed with unchanged deadlines. One full local race suite failed
the existing fluctuating-service account HTTP case with scheduler tick delays
up to 920 ms. These failures are retained; passing reruns do not erase them.
The failure reporter's concurrent child-log buffer access was separately fixed
so it cannot add a reporting-only data race to an application failure.
