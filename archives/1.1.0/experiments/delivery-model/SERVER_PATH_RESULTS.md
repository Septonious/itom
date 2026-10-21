# Current server-path comparison

Primary server network RTT is20ms, configurable via `ITOM_LOAD_SERVER_RTT_MS`; runtime controller timing remains measured. The new reference run is [39635387171](https://github.com/Septonious/itom/actions/runs/39635387171), commit844862d86d2b9cedb55847c2232a8372ee66e1bf. Earlier2ms results are historical and are not relabeled. Native production bytes and APK identity are unchanged by this test-only correction.

Both variants use the same corrected fixtures, actual supervisors/TLS/IMAP/fsync, ten accounts, independent client/server serialization clocks and original deadlines. Compilation precedes measurements; two reversed AB/BA invocations preserve all failures. Provider command service is a separate2ms fixture input, not server propagation. CI uses a shared4-core host for both endpoints and provider; it does not reproduce an independent8-core server plus a physical phone. No artificial server fault is injected. [Machine-readable results](results-server20.json) record log hashes, all available samples, completed rounds and failures.

## Idle and light requests

Each cell is pooled mean/p95/max application RTT in milliseconds,48 samples per phase and variant. These are two invocations, not48 independent experiments. All32B verified requests completed within the original3s deadline and retained their application sockets. Passing the request deadline is not proof of improved tails.

| Client path | RC2 idle | Model idle | RC2 light | Model light |
|---|---:|---:|---:|---:|
|5ms|102.6/182.6/215.2|87.8/172.5/224.5|93.8/121.8/164.6|98.4/256.5/317.3|
|40ms|140.5/202.8/294.7|132.8/188.5/371.4|147.4/204.0/389.9|122.6/175.6/196.1|
|180ms|362.3/467.7/589.0|362.8/449.4/458.6|368.6/446.5/588.4|336.3/408.6/456.3|
|changing40–180ms|233.7/390.8/536.0|214.3/384.0/756.9|211.6/395.6/419.2|214.3/394.9/488.0|

Mean light RTT improved on40/180ms client paths, but5ms light and changing-path maximum RTT regressed. A universal latency improvement or elimination of spikes is not established. Changing-path phase timing depends on request duration; raw per-request transitions are retained for the demand-transition case.

## Full-flight demand transitions

Four concurrent4MiB verified exchanges share the same short-request TCP before, during and after bulk. Original3s exchange deadlines and all socket/carrier identity checks remain unchanged.

At static40ms client propagation, both versions completed both invocations. Mean useful bulk rate was152.952Mbps per direction for RC2 and154.454Mbps for the model. Post-bulk mean/p95/max RTT was139.3/194.0/271.7ms versus127.6/196.8/250.6ms; p95 did not improve.

On changing40–180ms propagation, RC2 timed out reading a4MiB exchange in both invocations. The model completed both at84.199 and92.454Mbps per direction; post-bulk48 short requests had173.4/343.0/403.2ms mean/p95/max. There is no completed RC2 bulk rate or post-bulk distribution to compare in those failed runs. This is a limited laboratory stability observation, not proof of real Speedtest reliability.

## Worst observed raw4G

With the user's ten-ping trace,1Mbps down/2.5Mbps up and20ms server propagation, RC2 delivered0.273361Mbps and the model0.278400Mbps useful verified requests/responses in each direction over30s. Both completed64 exchanges without a failure or replacement socket/carrier and passed the unchanged0.1Mbps minimum. One invocation per variant is not a general performance comparison.

## Sustained throughput

Each case has four complete60s rounds per variant (two reversed invocations with two rounds each); mean useful upload/downloadMbps includes every round. Pass counts below are complete invocations satisfying all unchanged gates. No best-run selection or throughput-floor waiver is used.

| Case | RC2 mean up/down | Model mean up/down | RC2 invocations passed | Model invocations passed |
|---|---:|---:|---:|---:|
|SINGLE|154.101/210.936|158.327/213.909|1/2|0/2|
|MULTI32|183.574/248.947|181.253/247.044|2/2|2/2|
|retained SINGLE|126.625/230.108|117.859/235.835|1/2|0/2|
|retained MULTI32|141.150/198.884|145.800/196.179|2/2|2/2|
|changing40–180ms|116.827/246.159|116.697/248.403|2/2|1/2|

All40 sustained rounds completed with600 scheduled probes per variant, zero missed/timeouts, and20 preserved-identity records per variant. Round qualification passed17/20 for RC2 and14/20 for the model. The full workflow is correctly red; no failures were waived.

SINGLE model loaded maxima reached792/801/790ms in three rounds, exceeding the unchanged750ms ceiling; RC2 exceeded it once at766ms. One retained SINGLE model round had a one-second zero-upload stability window despite its full/tail rates exceeding100Mbps. Other retained SINGLE failures include87.137Mbps model and95.923/94.582Mbps RC2 verified floors. One changing-client model round fell to98.619Mbps in its final10s; RC2 passed all four changing-client rounds. Full errors and gate decisions remain in the JSON and raw artifacts.

Retained SINGLE mean upload was117.859Mbps for the model versus126.625Mbps for RC2 (about6.9% lower across these four rounds); other mean directions are near the reference, but the limited trials do not establish throughput equivalence. Retained MULTI passed both variants here. The old2ms retained-state ceiling cannot be treated as an invariant at20ms.

The present candidate is not accepted as a stable release or a no-regression result on the current server path. Further controller work must address retained-state startup/cadence and SINGLE fast-request spikes without shrinking sustained bulk solely because RTT rose. This correction changes the benchmark assumptions only; it does not silently retune or promote runtime code.

## Local check and limits

A separate local2-core-cgroup DEV3 invocation passed worst4G and idle/light deadlines at20ms server propagation, but light maximum reached1156.8ms. This was not a matched RC2 comparison and is not pooled into CI numbers. All220 production Go files match the actual DEV3 build; final1.0.0 and frozenDEV2 remain unchanged. ExistingDEV3 artifacts retain their original build evidence and experimental status. Server ICMP RTT alone cannot predict mailbox visibility, Android scheduling or fsync/provider tails.
