# FIELD55 — safe rollback + admission trace coalescing

Base: FIELD53, revision field32. FIELD54 field33 transport changes **completely excluded**: no broadcast wakeup and no 256KiB qualified ramp. Only source change affecting hot path is diagnostic aggregation of ordinary deferred admission events. Data scheduling, windows, fastRamp, journal, grants, wire and quota logic remain byte-identical to FIELD53.

`deferred_total` and `deferred_since_last` are cumulative and per-sample counts; `Line.DeferredAdmissions` gives total number from this lane in periodic health snapshots. Successful/waiting/non-deferred errors keep their original unthrottled trace events. Sample interval is 250ms per physical lane.

Status: experimental, full Go1.27.1, Android and real tests not done here. Field53 source is the preferred already observed rollback. Field55 is a low-risk field test; revert on any regression. Do not claim increased speed until real A/B. A failing FIELD54 trace is necessary to diagnose the original interruption.
