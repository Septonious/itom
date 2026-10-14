# ITOM 0.9.2-Rev2 transport candidate

Rev1 field measurements showed 1.75–7.87 Mbps download and unstable loaded RTT after expanding to fifteen accounts. Rev2 reduces durable publication from five transactions to three for local DATA and two for CONTROL, fixes independent DATA-reader discovery/observation, separates fast/bulk service feedback, and corrects drained-epoch, ACK utilization, cold placement and calibration order. Account RTT now identifies stale samples. Profiles still support more than ten accounts.

APK and Linux amd64/arm64 builds passed the local release gates: 788 core passing test events, mandatory outage recovery, 60 final race events, native tests, 71 Kotlin tests, and APK lint with zero errors. The debug signer matches the published Rev1 APK; clean local reinstall was authorized if needed.

**The complete 100/100 Mbps performance qualification failed.** Two sixty-second SINGLE repeats passed. The third averaged 118/103 Mbps but its upload tail fell to 99 Mbps. All three 24-stream MULTI repeats timed out on loaded probes. Real Android/provider Rev2 measurements were not run. This prerelease is a candidate; the speed goal and SINGLE/MULTI inversion remain unresolved.

Final regular field-derived stress diagnostics: fifteen-account SINGLE 163/105 Mbps, maximum loaded RTT 421 ms. Ten/fifteen eight-stream MULTI downloads were both about 49 Mbps; fifteen had slower upload and higher maximum RTT. Ten healthy accounts are a useful MULTI comparison baseline, while fifteen may benefit SINGLE and provide extra paths. These are model diagnostics, with stated assumptions and preserved full-tail failures.

Download the APK, server binary for your architecture, executable sources, or the full ZIP. The full archive contains findings, the refined rework plan, verification, checksums and test evidence. Preserve server journals/wallets and physical mail debt during upgrade. Raw uploaded diagnostics, credentials and signing keys are excluded.
