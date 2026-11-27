# ITOM 1.5.1 RC14 development checkpoint — UNQUALIFIED

Based on RC13 commit 3e85b2c19b32cbbe936656e8592568bf670a0bff.

- Directional receipt-driven pacing, active bandwidth probing, delivery-window adaptation and bounded drain/recovery; authenticated optional downstream demand hints remain compatible with legacy peers.
- Five-second cellular decisions, four-account floor, and moderate-RTT download expansion only with fresh healthy local evidence and remote progress.
- Protect exact S2C-01 folder components, including descendants, from purge; bulk import and persistent manager submenus; candidate revision inspection without executing uploaded binaries.
- Direct clipboard profile import; retain RC13 Home/Profile and IPv4-first identity changes.
- Consolidated parallel cached Actions workflow; release publication requires all mandatory checks. Manual topology qualification models client/Rambler/RU server and configurable encrypted exit delay.

Local Go 1.27.1, restored offline cache. Focused controller, pacing, purge, manager, provider and Python checks passed. Full mailstream race run failed a control-mailbox recovery assertion and was later killed; isolated reruns passed without changing that test. Full core/native/qualification compilation is blocked by missing golang.org/x/text v0.42.0 in offline cache. Android SDK and real routing dataset not available locally. Simulated recovery throughput is not a field speed qualification. No release Actions until full local Go gate passes. Checkpoint only; do not deploy as a qualified release.
