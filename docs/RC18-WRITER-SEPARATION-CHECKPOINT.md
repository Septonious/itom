# ITOM RC18 research checkpoint — NOT release-qualified

Exact recovered Writer Separation source from ITOM-RC18-writer-separation-checkpoint.zip. Source tree: 495bef8681b66cd162d468d22549467f085e34dd (generated Python bytecode excluded). Parent: UI-restored RC16 field source 58da8299e08b2b8c65ea2d4a3b81ad6d98a5ad5c. RC7 remains the reference. The executable revision intentionally remains itom-1-5-1-rc16-dev; this is a research checkpoint, not a release or a field-build request.

Retains restored Android UI, RC16 mobile recovery, per-frame retransmission counters, shared physical control-writer debt accounting, and quiet desktop feedback publication via an idle DATA writer. RC17 polling backoff and bounded spill are excluded. The stricter Go/Python home qualification includes 50 kbps light bursts, maximum RTT <=200 ms, P95 <100 ms, verified uploads/downloads, >=200 Mbps sustained goodput each direction, and required account-pinned evidence. The gate was not relaxed.

Recovered completed measurements:
- Delayed-provider encrypted screen: session nearest-rank P95 178 ms, account-pinned P95 213.83 ms, 253.636/254.705 Mbps, 8/8 downloads and 8/8 uploads, maximum upload receiver gap 67.774 ms. Failed strict latency qualification.
- Two-cycle clean direct: 273.82/304.35 Mbps, 16/16 verified transfers, maximum application RTT approximately 153 ms, maximum account RTT approximately 175 ms. Failed strict P95.
- Two-cycle clean encrypted: 280.77/294.39 Mbps, 16/16 verified transfers, maximum application RTT approximately 193 ms, maximum account RTT 199.6 ms. Failed strict P95.
- Archived targeted race tests passed three repetitions; full cross-module suite, repeated sustained matrix, cellular and physical-network qualification remain outstanding. No fresh benchmark or race sweep was executed during checkpoint publication.

The interrupted subsequent session reported full/ranked/demand-driven isolation trials and a visibility-vs-tagged-APPEND-completion diagnostic. Their exact source, raw captures and patches were not recovered in the present workspace. They are not included as tested changes in this commit. Continue from this recoverable source rather than assuming those unarchived changes were saved.

The original Writer Separation archive includes all six independent experiment patches, raw traces, binary hashes, reproduction scripts and strict-gate logs. No workflow configuration is changed. No Actions should be launched from this checkpoint.
