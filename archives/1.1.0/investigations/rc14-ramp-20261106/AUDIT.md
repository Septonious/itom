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
