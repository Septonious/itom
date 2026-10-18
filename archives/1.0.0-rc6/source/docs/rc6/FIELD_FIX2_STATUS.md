# RC6 FieldFix2: verified status, 2026-10-18

**Do not publish as RC6 replacement.** The tag v1.0.0-rc6 still refers to the older, laboratory-qualified source. FieldFix2 source build is 5c93999818ee41b20eae57d72e113bf8e0990c6c (build branch adds CI metadata).

Evidence:
- Paired Android and server traces 2026-10-17: ten authenticated accounts, successful provider APPEND during burst, excessive short placement on one account; client TCP errors do not uniquely identify initial cause.
- FieldFix1 corrects advisory fast-mailbox placement and keeps long-lived TCP sockets; 8MiB inflight failed <750ms in 32-stream 150Mbps runs.
- FieldFix2 additionally caps aggregate outstanding logical DATA at 4MiB. CI sustained 32-stream, 10-account, 150Mbps, 60 seconds PASS: upload 40.545, download 31.947 Mbps; maximum loaded RTT 620.982ms, 30/30 probes, 65 unchanged original sockets, positive duplex throughput in all 60 seconds.
- Android build (82 unit tests, lint) and Linux amd64/arm64 builds PASS; CI APK and binary SHA-256 separately verified.
- Downshift test initialized carrier at 150Mbps before creating dozens of sockets, then immediately cut to 10Mbps. This FAILED and was initially misdescribed as a speedup regression.
- Corrected **early** mode starts on the first profile step before application OPEN, and distinguishes 10->20->50->150Mbps from an abrupt high-to-low downshift. Neither scenario has passed all strict gates on FieldFix2.
- Local 4-stream true early profile cold-only tests: at 10Mbps, the 4MiB setting failed a startup probe; at 20Mbps it passed two probes with 666ms max. A 512KiB max experiment passed the 10Mbps two-probe cold test, but has NOT passed the 60s 32-stream, full recovery, or baseline suites and must not ship.
- No change to physical mail quota, provider session allocation, durable journal, saved accounts, app ID or TCP stream identities. Live Android/Rambler field qualification remains outstanding.

Release gate: full Go correctness/race/recovery; matched Android/Linux APK binaries; original 12-case fresh+retained lab matrix; repeated 32-stream 150Mbps steady, correct low-to-high speedup and separate downgrade with 0 probe timeouts, 0 zero-throughput seconds, max loaded RTT strictly below 750ms, original socket survival; then actual Android/Rambler qualification. No silent RTT tolerance or test exit-code masking.
