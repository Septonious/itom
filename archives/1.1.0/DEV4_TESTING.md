# ITOM 1.1.0 DEV4 testing candidate

Selected conservative empty-bulk refill trigger with original 1500 ms periodic maintenance. This is the new testing RC, not a stable release. Stable 1.0.0/RC2 remains unchanged; the previous DEV3 source is frozen in checkpoints/DEV3/source.

The controller uses useful delivery to grow bulk flight while scheduling sparse responses separately. A fenced grant release can request existing bounded proof service when no bulk permission remains. It cannot create grants, release debt, bypass FIFO, or acknowledge delivery. Production timings use measured feedback; 20 ms is the comparison's server propagation, not a runtime constant.

## Evidence

Selected recipe: experiments/refill-baseline-cadence/stage.py (ITOM_REFILL_TRIGGER=empty). Performance source commit 4c8de0ee2177aed84eaac1929e7bb168d171f56b, [run 39685864723](https://github.com/Septonious/itom/actions/runs/39685864723). Full Go suite, vet and accounting/controller races passed in job 119519621717. Packaging repeats focused contracts after the version-only edits.

Original sustained performance gates: candidate 20/20, RC2 16/20, with the same fixtures and thresholds. Each variant completed all planned rounds; failed reference invocations remain in results.json. Some workflow jobs are red because they include failed RC2 comparisons; this is not an all-green workflow claim. Candidate short-request, retained interactive, full-flight transition and worst-4G checks passed. These are simulated IMAP tests, not real Rambler or Android measurements; CI has four cores, deployment has eight.

Retained upload-only: 158.2 vs RC2 76.9 Mbps. Download-only capacity remains about 269 vs 266 Mbps. Retained 40 ms short-request mean improves 168.7 to 134.7 ms (p95 356.3 to 198.7 ms). Changing-path light-load mean improves 210.9 to 200.3 ms. RTT is tunnel request/response time, not direct IMAP latency.

## Remaining limitations

Retained simultaneous-duplex download is 8.4% lower in MULTI and 13.0% lower in SINGLE; upload improves. Some idle/light tails still regress, including a fixed-40 ms fresh idle maximum of 522.5 vs 228.6 ms. This build does not meet a universal no-regression or spike-free claim. It is selected for field testing because it has the strongest completed gate result and full correctness proof, not because every original development goal is met.

Preserve server private state, journals, wallets and app data. Android package remains org.itom.client, versionCode 10104, versionName 1.1.0-dev4-debug. Actions uses persistent signing secrets if all four are configured, otherwise a temporary debug signer. Do not erase wallet data to work around a signature mismatch. Client and server should both use this candidate. Previous DEV3 build metadata remains historical.
