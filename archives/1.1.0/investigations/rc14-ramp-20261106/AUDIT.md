# RC14 short-transfer and repeated MULTI field audit

Screenshots report 36.0/40.2, 46.3/55.1 and 71.3/38.2 Mbps down/up.
The user observes late ramp-up, abrupt speed changes and a worse second MULTI run.
Raw exports contain older RC11–RC13 rotations; only RC14 samples inform this audit.

The current session keeps all carriers, with no account failures or cleanup
quarantines. Client recovery begins with approximately 606 MB of conservative
old debt. Most clears during the first 32 seconds; about 4.9 MB remains deferred.
Server DATA publication rises from 13 to 100 Mbps over roughly fourteen seconds.
A later download restarts near 7 Mbps and peaks near 50 Mbps. The delivery
controller repeatedly relearns from its 512 KiB floor after quiet/light intervals.
Upload flight grows beyond 12 MB while receipt RTT later exceeds 1.8 seconds.
The old upload-only controller uses queued RTT to expand flight, and a short
receipt sample can sharply reduce its rate estimate. Download uses a separate,
fixed-horizon behavior. These mechanisms explain implementation risks; the trace
does not uniquely attribute every field fluctuation to a specific command.

The server also excludes as many as five of ten APPEND writers for housekeeping
while only one mutation lease is active. The lease does not bound preparation:
each account independently stops publication to drain terminal APPEND replies.
Later load therefore has fewer publishing accounts even without disconnections.

RC15 applies compression-safe bulk feedback in both directions. ACK-clocked
flight growth is allowed only with low queue delay, a short peak filter prevents
one sparse receipt batch from halving capacity, and target flight uses minimum
bulk RTT plus bounded headroom rather than growing with queued RTT. The initial
512 KiB flight and physical/shared-memory caps remain unchanged. Cleanup can
borrow at most two writers including preparation; a slow drain still does not
own the single mutation lease. Accepted controls retain priority.

Independent 8-second FIFO/batched-receipt scenarios and three simultaneous
APPEND-drain attempts fail on frozen RC14 and pass on the candidate. Full-path
qualification must additionally measure short SINGLE/MULTI runs, first seconds,
rolling delivery and post-transfer recovery; long-phase averages alone are not
sufficient. Cold old-debt results remain separate and cannot claim immediate
100 Mbps before audited capacity is available.

Admin category cards use the row's intrinsic height and fill that shared height.
Descriptions wrap naturally while paired cards retain matching geometry and the
existing gradient, typography and colors. Android build/lint validation remains
required; no physical-device layout measurement is claimed.

The first 16-stream, 10-second full TLS replay with 120 ms per-command service
exposed a remaining failure: uploads averaged 23–26 Mbps and downloads 45–61 Mbps.
Byte-clocked growth alone did not fix it. A bounded feedback-batching prototype
improved download but not upload; all results are retained. This 120 ms *idle*
APPEND hypothesis also exceeded the 300 ms idle RTT target and is not presented
as a measured provider latency. The main 60 ms service model independently
produces larger loaded completion times through ordinary queued commands.

Tiny chunk/TLS trailers arriving more than two milliseconds after a bulk write
were being promoted to mirrored interactive control traffic. The candidate keeps
these adjacent writes on the bulk path for 250 ms; unrelated new streams and
isolated requests still use the immediate path. Receipts and WINDOW packets can
share two-millisecond bounded priority MIME bursts. The first 512 KiB flight is
spread over small publication quanta across ready accounts instead of filling
only two writers. The initial flight remains unchanged. These changes require
short full-path qualification, rather than relying on the controller simulator.

The TLS classifier now also counts consecutive small writes: the first 4 KiB of
an isolated burst stays immediate, while a train of initially small TLS records
becomes bulk. A short counter reset preserves paced small messages. Hedging is
retained for isolated requests but suppressed within a simultaneous interactive
burst. Dedicated regressions preserve isolated/paced treatment and demonstrate
both old faults on frozen RC14. A 1 MiB initial-flight experiment added no useful
short-run gain and was reverted; the final initial flight remains 512 KiB.

The resulting local 16-stream, 10-second replay completed three pairs at
113.0/104.0/104.3 Mbps upload and 173.7/179.5/179.8 Mbps download. Maximum
idle/post RTT was 261.6 ms and loaded RTT 512.1 ms; at most two writers were
borrowed, with no failures or quarantine. Every rolling two-second interval
starting after second two exceeded 100 Mbps. Rolling one-second upload reached
100 Mbps at 3.0/3.5/3.5 seconds, so the two-second ramp target remains unmet.
These local averages are not field qualification. Matched hosted short and long
replays, complete race suites and Android compilation/lint remain required.

The bounded held-bulk test needed 128 packet slots instead of 64 to reach its
unchanged byte-flight assertion after TLS-sized records became bulk. Its payload,
shrunken-flight condition, fresh TLS request and byte-integrity assertions remain
unchanged. The targeted race cohort passed that condition and verified recovery.

The matched hosted short gate passed on source 410ac36. Frozen RC14 uploads
were 92.8/86.3/86.1 Mbps, with rolling one-second 100 Mbps reached at 4.5/5.0/4.5
seconds and up to six borrowed writers. Candidate SINGLE uploads were
119.6/115.3/116.2 Mbps. MULTI uploads were 115.2/104.2/106.4 Mbps and downloads
175.7/182.2/176.0 Mbps. Its upload ramp was 3.0/3.5/4.0 seconds; the two-second
target remains unmet. Every candidate rolling two-second interval starting after
second two exceeded 100 Mbps. The MULTI minimums were 129.9/110.4/105.9 Mbps for
upload and 192.9/195.3/165.2 Mbps for download. Peak borrowed writers stayed at
two, maximum idle/post RTT was 233.1 ms and loaded RTT 465.0 ms.

The separate long repeated-cleanup gate completed uploads at 129.4/128.3/117.2
Mbps and downloads at 186.8/145.7/135.1 Mbps. A cold replay audited all
534,773,760 bytes of old debt, then reached 128.2/155.9 Mbps upload/download.
These are independent synthetic results with retained strict misses, rather
than claims about the next real-provider run.
