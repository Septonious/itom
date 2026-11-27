# ITOM 1.5.1 RC14 development candidate — local Go gate passed

Based on RC13 commit 3e85b2c19b32cbbe936656e8592568bf670a0bff.

- Directional receipt-driven pacing, active bandwidth probing, delivery-window adaptation and bounded drain/recovery; authenticated optional downstream demand hints remain compatible with legacy peers.
- Five-second cellular decisions, four-account floor, and moderate-RTT download expansion only with fresh healthy local evidence and remote progress.
- Protect exact S2C-01 folder components, including descendants, from purge; bulk import and persistent manager submenus; candidate revision inspection without executing uploaded binaries.
- Direct clipboard profile import; retain RC13 Home/Profile and IPv4-first identity changes.
- Consolidated parallel cached Actions workflow; release publication requires all mandatory checks. Manual topology qualification models client/Rambler/RU server and configurable encrypted exit delay.

Local Go 1.27.1 and repaired offline cache: all four module dependency checks and full race suites passed. Core 78, native 41 plus actual packaged routing fixture 1, qualification 2, mailstream 130 top-level test passes; Python 6/6. Full mailstream race run 398.437 seconds (including previous control-mailbox recovery failure and writer keepalive recovery). No test assertions changed for this rerun. Routing fixture extracted from the checksum-verified RC13 build. Source tree 8f279e1868b762af3d2e0ed985eb4fb3e6a3e12e. Android compilation and unit tests require Actions; actual cellular/desktop performance qualification remains outstanding. This is a development candidate, not a field-qualified release.
