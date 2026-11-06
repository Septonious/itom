# RC18 — first release reliably meeting the goals

The user accepted RC18 on 7 November 2026 after two field measurements:
109/131 and 143/144 Mbps download/upload, 153/161 ms idle ping and 11/16 ms
speedtest jitter. Matched captures keep all ten accounts/thirty roles healthy,
with zero DNS failures or worker refusals. This is the new working baseline.

The remaining work is diagnostics accuracy and lower interactive RTT. Per-account
byte counters are populated, but the diagnostics screen misses the service's
calculated rates. Account stream counts overlap because streams are striped;
transport streams also include opening connections. These need distinct labels.

The minimum observed interactive receipt RTT is 133 ms; a single-account IMAP
command latency is not the full tunnel RTT. Sub-100-ms results remain a target.
General UDP is currently blocked, so lower TCP RTT alone does not establish
mobile-gaming compatibility.

The acceptance record is separate from the original build reports and artifacts.
No source tag, APK, server binary, signature or historical build claim is changed.
