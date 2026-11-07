# Rev2 field findings, account scaling and actual outcomes

The supplied screenshots and rotating traces establish a Rev1 download regression and unstable loaded RTT. Rev2 addresses several reproduced runtime failures, but the complete real-world 100/100 Mbps target remains unqualified. The following numbers are laboratory diagnostics; they are not predictions of phone Speedtest performance.

## Fifteen versus ten or fewer accounts

Final runtime: `4f9c0cb`. Fresh public client/server supervisors, one stream per direction for SINGLE and eight for MULTI, 20 seconds measured after a separately recorded ten-second cold ramp. Per-account/per-endpoint wrapper quantiles were derived from the supplied traces; the regular-service stratum excludes samples above 500 ms. Server commits add 12 ms and client control commits 0.5 ms, retaining actual fsync. The 600 Mbps cap, missing DATA-FETCH proxy and absent temporal correlation are model assumptions.

| Accounts | Mode | Upload Mbps | Download Mbps | Maximum loaded RTT ms | Missed/timeouts | 100/100 point target |
| ---: | --- | ---: | ---: | ---: | --- | --- |
| 5 | SINGLE | 70.477 | 30.068 | 401 | 0/0 | Not met |
| 5 | MULTI | 67.043 | 26.031 | 765 | 0/0 | Not met |
| 8 | SINGLE | 125.541 | 62.975 | 382 | 0/0 | Not met |
| 8 | MULTI | 68.564 | 35.835 | 770 | 0/0 | Not met |
| 10 | SINGLE | 151.178 | 83.703 | 465 | 0/0 | Not met |
| 10 | MULTI | 70.936 | 49.336 | 847 | 0/0 | Not met |
| 15 | SINGLE | 162.936 | 104.923 | 421 | 0/0 | Met in this diagnostic |
| 15 | MULTI | 59.979 | 49.165 | 913 | 0/0 | Not met |

All eight final-source cases completed their 20-second useful-byte interval without a probe timeout or missed probe. That passes their measurement/stability check, not the 100/100 speed goal. The point-target result is separate from sixty-second qualification.

At fifteen accounts, SINGLE download was 25.4% higher than at ten (104.9 versus 83.7 Mbps), with about 50% more default provider connections. Eight and five accounts delivered materially less. In MULTI, download was effectively unchanged at fifteen (49.2 versus 49.3), upload fell about 15.4% (60.0 versus 70.9), and maximum RTT rose (913 versus 847 ms). These are single diagnostic runs, with heterogeneous subsets; repeat noise and subset quality prevent a universal account-count conclusion.

**Recommendation:** ten healthy accounts are a reasonable MULTI comparison baseline; fifteen are worth retaining as a SINGLE capacity option and additional failure paths. The diagnostics do not justify buying/adding accounts merely to improve MULTI. Keep all fifteen configured if desired: Rev2 has no ten-account ceiling and does not remove or truncate accounts. A matched phone/provider experiment must decide whether the extra background readers, logins and cleanup pay for themselves.

## Matched Rev1 comparison

The published Rev1 algorithm at `a1a1806` was run with the same provider model, positive commit-delay helper and measurement harness. Rev2 results in this table come from the initial candidate `824fbe8`; final cold-placement/calibration corrections followed.

| Accounts | Mode | Rev1 upload/download Mbps | Initial Rev2 upload/download Mbps | RTT/outcome |
| ---: | --- | --- | --- | --- |
| 10 | SINGLE | 140.929/19.163 | 145.936/74.147 | Both completed the 20-second interval; initial Rev2 max 380 ms |
| 10 | MULTI | 111.383/12.339 | 76.939/44.499 | Rev1 stopped early on a loaded timeout; initial Rev2 max 867 ms |
| 15 | SINGLE | 188.075/17.577 | 156.330/89.863 | Both completed the 20-second interval; initial Rev2 max 629 ms |
| 15 | MULTI | 60.154/15.676 | 58.012/47.815 | Rev1 stopped early on a loaded timeout; initial Rev2 max 750 ms |

Rev1 SINGLE download completed at about 19.2 Mbps with ten accounts and 17.6 with fifteen, versus 74.1 and 89.9 in the matched initial Rev2 candidate. Rev1 MULTI runs timed out after seven/twelve seconds, so those truncated means are not matched sustained throughput averages. Initial Rev2 completed both intervals. This supports the diagnosis of publication/control overhead and shared feedback failures under the declared assumptions; it does not establish field improvement.

## Slow ramp and finite speed-test phases

Rev2 resets a genuinely drained congestion epoch, preserves real short-queue protection, consumes ACK-time utilization once rather than missing a drained flight, exposes cold pending fsync to placement, and chooses initial cold calibration independently of account arrival order. First-second byte windows are retained before any warm-up exclusion. The following are five-second cumulative useful download rates, not peak samples. Application OPEN/bootstrap precedes this bulk clock and is not included.

| Accounts | Mode | First 5 s download Mbps | First 10 s download Mbps |
| ---: | --- | ---: | ---: |
| 5 | SINGLE | 49.336 | 43.830 |
| 5 | MULTI | 54.316 | 43.778 |
| 8 | SINGLE | 74.034 | 73.114 |
| 8 | MULTI | 78.538 | 64.697 |
| 10 | SINGLE | 73.191 | 82.890 |
| 10 | MULTI | 80.688 | 74.534 |
| 15 | SINGLE | 55.942 | 86.376 |
| 15 | MULTI | 80.006 | 72.221 |

The cold-start fixes are covered by deterministic reproductions and the early useful-byte measurements. They do not prove that the phone Speedtest will now display a final value within every test phase. A timer ending without data, incomplete progress or a late probe failure must remain a failure.

## Full-tail stress and limits

The full-tail ten-account SINGLE case, retaining observed wrapper tails up to roughly 2.8 seconds, failed after seven measured seconds with about 58.7/71.7 Mbps and a loaded probe timeout. It is preserved in the archive. Those wrappers are not deconvolved wire service times, and the DATA-FETCH distribution is missing. The model is not calibrated to reproduce the real provider; the separate regular stratum must not conceal this failure or qualify field RTT.

A later uniform-provider MULTI check still reproduced weak download and high RTT. SINGLE/MULTI inversion remains an unresolved performance limitation of the candidate. More accounts and passing correctness tests do not solve it.

## Strict qualification and builds

The strict uniform-provider fifteen-account repeat set failed overall. SINGLE means were 125.3/105.7, 132.0/102.7 and 118.2/103.0 Mbps; loaded maxima were 489, 448 and 423 ms with all sixty probes observed and overlapping. The third upload tail was 99.0 Mbps, below the strict 100 Mbps floor. The first two SINGLE repeats passed. All three twenty-four-stream MULTI repeats failed on a loaded probe timeout. This is an actual failed qualification, not an unrun gate.

See `REV2_VERIFICATION.md` and `verification.json` in the full release for the executed sixty-second repeat results, required correctness/outage/race checks, Android/native tests, lint, artifact identities and hashes. A skipped, missing, failed or incomplete performance case never qualifies. Android device and real-provider Rev2 measurements are NOT RUN.

The APK uses the local debug signer as authorized by the user. Preserve server journals/wallets and their physical mail debt. Reinstall the Android app locally if needed, backing up profiles you want to keep.

## Evidence provenance

Only numerical pseudonymized field observations and synthetic test traces are distributed. Raw uploaded diagnostics, account mapping, user/session identifiers, credentials and signing keys are excluded. The initial 20-case matrix and final eight-case matrix preserved unchanged source hashes within every run. Logs include failures and reproduce-before-fix tests.
