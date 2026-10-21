# Bounded light-response search and fresh startup evidence

This candidate starts from the exact DEV3 grant-aware model and the same immutable RC2 production comparator. It is an experiment until its matched results are collected. Server network RTT defaults to20ms and remains configurable; runtime timing comes from measured unloaded IMAP command service. Existing qualification gates, durations, deadlines, probes, quotas and identity checks are unchanged.

The reader uses a measured empty-poll interval (one quarter of direct command RTT, bounded2–20ms) only while the existing bounded request/response hot window is active, notification support is available and DATA/maintenance are independent. During that window it does not add an early IDLE/DONE command turn after three empty FETCHes. Visible rows still drain immediately. When the hot window ends, the existing notification-driven quiet wait and missing-notification audit resume. Unknown timing, unsupported IDLE and shared services retain their original polling bounds. This changes observation scheduling, not liveness/reconnect deadlines or provider visibility proof.

A drained bulk epoch retains bandwidth history but clears its previous startup plateau and pending capacity-drop episode. Low delivery samples while the new epoch is still growing below its historical delivery BDP are window-limited evidence, not a confirmed new bottleneck. Full-window capacity drops and collective stalls remain covered by the unchanged model tests. No RTT-only bulk halving is restored.

Only three production files differ from DEV3: `tunnel/io.go`, `tunnel/response_wait.go`, `tunnel/throughput_model.go`. The recipe writes production hashes and identical shared-fixture provenance; `previous/` preserves DEV3 for ablation. Final1.0.0, current DEV3 source and its built artifacts are untouched during evaluation.

Reproduce with Go on PATH:

```
python3 archives/1.1.0/experiments/light-response/prepare.py /tmp/itom-light-response
```

The workflow `itom-v110-light-response.yml` runs the same five sustained scenarios, two reversed invocations and two complete60s rounds per invocation (20 rounds per version), plus matched idle/light paths, bulk-to-quiet transitions, worst4G and targeted correctness/race checks. Loaded SINGLE RTT spikes remain reported under the original750ms gate. The optimization objective is idle/light latency and bulk bandwidth; the candidate must not reduce speed to mask a loaded-RTT failure.

First completed run39641704294:14/20 performance rounds passed for both RC2 and candidate. Fresh SINGLE improved from3/4 to4/4 accepted rounds and its mean useful rates increased, but all retained SINGLE rounds missed100Mbps for both variants. Idle means improved overall; light means/tails were mixed across paths and host variability was substantial. [All samples and failures](results.json) remain recorded. This is not the requested17/20 result and is not promoted.
