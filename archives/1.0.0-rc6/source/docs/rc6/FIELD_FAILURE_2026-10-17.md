# RC6 live-provider field regression, paired traces, 2026-10-17 UTC

This branch is an **experimental field stability fix**, not a certified Android release. Existing RC6 tag and release binaries are unchanged.

- Server export: 118,928 trace events; client export: 60,629.
- All ten accounts authenticated; during 23:06:47-23:06:53Z physical server DATA APPENDs and controls continued succeeding. No broad provider LOGIN/APPEND errors were observed.
- Android client 473 physical DATA batches: 464 fast, 9 bulk; 291/464 fast batches took a03 despite 10 connected accounts.
- Server 858 physical DATA batches: 614 fast, 244 bulk, distributed much more evenly.
- Client app TCP resets clustered near 23:06:52Z following traffic acceleration. Application resets are not proof of an original underlying failure.
- Original 4-stream RC6 synthetic qualification passes; new 32-stream, 10-account, 150Mbps synthetic load on unmodified RC6 **failed around 21s**, after a timed-out overlapping application RTT probe.
- Experimental 1s advisory fast-placement cost avoids pinning independent short OPENs to a03; no physical quotas, journals, TCP identity, logical credit, or fast ACK accounting changed.
- First 32-stream local repeat with this code completed 60s, all 30 probes returned, 65 original sockets survived, but worst RTT was **1,099ms**, above the existing strict <750ms release target. **Do not release based on this.**
- New regression gate deliberately retains <750ms, all 60 seconds positive duplex, zero missed/error probes and same sockets. Fix cannot be certified before this gate and the existing full CI suite pass; real phone+Rambler validation is independently required.
