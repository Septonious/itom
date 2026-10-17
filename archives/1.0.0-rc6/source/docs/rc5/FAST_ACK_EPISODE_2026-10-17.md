# RC5 fast-ACK pressure episode A/B (2026-10-17)

**Unqualified local experiment; transport change rejected.** No journal/account upgrade, release, binary or Android installation was made.

The RC5 10-account investigation found a real feedback loop in the server24/client12 laboratory run: a single aged short DATA range could keep the upload-side `fastPressureUntil` set while newer short and bulk authenticated ACKs had already progressed. An isolated copy added `FastProgress` (true only when a freshly authenticated fast logical ACK was observed within the last 1s) to `deliveryFeedback`. Under persistent fast-age/pressure, the existing delivery policy performed its initial guarded 50% reduction as before, but suppressed *further* reductions while both new fast and bulk ACKs were continuing. True bulk no-progress for >=3s still triggered the existing OUTAGE reduction. A focused new test passed initial reduction, conditional suppression, renewed reduction after fast feedback stops, and mandatory no-progress reduction.

**Model test:** 10 accounts, 24 streams per direction, 60-second concurrent useful transfer, 80 ms modeled provider commands, 600 Mbps transport capacity, 4 Go execution threads, fresh client12MiB/server24MiB physical journals (the 24MiB server journal was added only to the disposable test copy; the released/committed production allowlist is unchanged).

- Download **106.269 Mbps**, upload **39.099 Mbps** (full 60 seconds).
- Loaded RTT median **313ms**, p95 **403ms**, maximum **503ms**, all 30/30 overlapping probes successful and zero missed/timed-out probes.
- Upload dropped to roughly 5–11 Mbps per 5-second block in the second half despite download exceeding 100Mbps. The 60s strict 100/100 case FAILED.
- No successful evidence of restored symmetric capacity. The local experiment and added delivery-feedback field **were NOT committed**. Original controller behavior remains authoritative in the RC5 GitHub source.

The distinct `AccountSessions=5` experiment has already been rejected: the application partitions account provider budget across endpoints by default, and this configuration produced 41.117/14.143 Mbps down/up with 1011ms max RTT.

**Next investigation:** rather than mask a real receive service delay with fewer controller reductions, correlate authenticated upload data ARRIVAL on the server with control ACK sealing/publication and client ACK application, at a per-account timestamp granularity. Keep legacy wallet, quota, every unknown APPEND obligation, encryption, TLS and deletion proof unchanged.

No release until three complete fresh+retained SINGLE and MULTI repetitions with 10 accounts all sustain **>=100 Mbps in both directions**, RTT <750ms and after-load connection survival, followed by separate real-device/provider validation.
