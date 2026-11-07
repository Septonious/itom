# Rev3 verification

Correctness and builds: **PASS**. Full sustained100/100 laboratory performance gate: **FAIL**. Field qualification: **NOT RUN**.

- Core:19 packages,803 passing test events, no failures. Thirteen optional tests skip in the normal suite; all mandatory unstable-provider survival cases execute. The production load harness and strict qualification execute separately in this release evidence.
- Focused race:59 passing tests across journal, wallet, datapath and tunnel, no failures or skips.
- Bundled IMAP fork checks/tests, module checks and Go vet pass. Linux amd64 and arm64 ELFs build.
- Native host tests pass; Android native libraries build for arm64-v8a and armeabi-v7a.
- Kotlin domain tests:71 pass, no failures/errors/skips. APK lint:zero errors, one pre-existing ModifierParameter warning in Theme.kt111. Release Kotlin compilation and debug assembly pass.
- APK identity:org.itom.client.dev.rev7,9203,0.9.2-Rev3-debug. APK v2 signature verifies. CertificateSHA256:c9c766e99b9b691044bd7fd2a5613ceb15fb3266f87aee225a84f56f160a8e45, matching the published Rev2 debug signer. No private key is included.
- Core runtimeSHA256:49016a6a2912acdc3a2418ff0649a85b9271243502157f16f999d900314bd726. Final qualification source fingerprint remains unchanged through builds:30b9ca7e0b257a557430b2f4aa9dde1198e2725d5e1546bcd9a8e22b9d5915ea.
- Six60second intervals complete with all360 probes overlapping useful traffic, no missed/timeout probes, and maximum observed application RTT below511ms. Two MULTI cases pass the full speed gate; the third and all SINGLE cases fail throughput. See findings for whole and30second-tail numbers.

Go tests, TLS/IMAP fixtures, model diagnostics and host native tests do not qualify real Android/provider performance. Device tests and field Speedtest remain required. APK and server artifacts are implementation candidates, published as a prerelease with the actual failures retained.
