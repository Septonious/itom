# RC13 selection and same-fixture controls

All load runs are serial, on the same shared three-CPU process host, with independent client10ms/500Mbps and server20ms/1000Mbps paths, provider service2ms, ten accounts/twenty lanes, real TLS/IMAP and fsync. After startup the server journal gets the same measured20ms durable-commit service-time replay in every variant. This is not a blackout/faulty-server test and not an eight-core CPU emulation. The common phased workload uses eight streams and a persistent32-byte request channel, with unchanged8-second OPEN,45-second transfer and3-second small-request deadlines. Every transferred byte and the upload tail acknowledgement is checked.

## Original 4 MiB-per-stream workload

| Variant | Download/upload Mbps | Short request mean/max ms | Outcome |
| --- | --- | --- | --- |
| Original RC2 |25.491/28.770|561/2884|FAIL: small request timed out; the data phases completed|
| RC12 |66.029/41.509|232/461|PASS|
| Early bulk-refill experiment |59.593/37.928|234/546|PASS, rejected for lower measured throughput|

RC2 production is original tag v1.0.0-rc2 at a50a6a9e31ee9b38b53e84c9dce645e7ed7e415b: all238 non-test files match the prior verified production manifest, copied in rc2-production-sha256.json. Relevant supervisor/network/TLS fixtures are byte-identical; the shared supervised-load file has the prior optional diagnostic-only compatibility shim. The workload file itself was copied identically. These cases retain7.5MiB/150 old remote ordinals per account with no old client wallet. They cannot be compared directly to the earlier fresh/retained duplex RC2 healthy-reference results. RC2 remains a valid control; this specific newer missing-wallet/journal-cost workload exposes a short-request failure on RC2 too.

The early bulk-refill patch remains in Git history (510784db51eb6338d920cc3c39e49f68f4e492a2) and its regression in REJECTED_BULK_REFILL_TEST.go.txt. It is not packaged. Final lane.go is byte-identical to RC12, including its fast-only pre-fence refill behavior.

## Selected reserve-only candidate, longer workload

The common test was extended to16MiB per stream (128MiB per phase), with actual receive-side five-second progress samples. Candidate then RC12 were run serially; their common workload files are identical. Retained seed/routes/service cost/deadlines are unchanged.

| Variant | Download/upload Mbps | Short request mean/max ms | Outcome |
| --- | --- | --- | --- |
| RC12 |66.246/30.425|289/1769|PASS|
| Final reserve-only RC13 |76.735/35.464|249/1125|PASS|

Candidate upload five-second sink samples:35.652,30.199,38.168,39.007,38.168,33.554Mbps. RC12:33.135,39.846,31.038,33.974,28.526,30.262,17.636Mbps. No stream failed. These are a single pair; host/fsync variation prevents attribution of every rate difference to the reserve change. In this7.5MiB seed the severe-byte threshold is not active, so treat this as a regression check, not a claimed throughput mechanism. The field-like small-capacity branch is separately verified using exact charges and real supervisors.

The selected ten-account TLS test restores20/20 directions with real TCP/512KiB bulk/short traffic on the three affected accounts; old grants remain byte-for-byte unchanged and audits pass. The unit case simultaneously reserves a full local bulk BODY and bounded fast BODY while the incoming classes stay allocated. This does not erase missing wallet history, certify real Speedtest reliability, eliminate every RTT spike, establish sustained100Mbps, or waive any full qualification threshold.
