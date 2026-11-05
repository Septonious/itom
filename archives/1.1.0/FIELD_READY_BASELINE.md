# RC10 — first field-ready candidate

On November 5, 2026, the user confirmed MAILSTREAM-RC10 as the first field-ready
candidate after real-world testing. Preserve this build as the reference for
further upload improvements. This is a testing-candidate designation, not general
production qualification for every network or provider condition.

- Release: https://github.com/Septonious/itom/releases/tag/v1.1.0-mailstream-rc10
- Frozen source: `b6d53de7ec97bef2d90582186a8398e409eed283`
- APK SHA-256: `3cf9c4620712e22f739587f562d850b0a434fe86caf9134b8f71f621272079a3`
- Android version code: 10135; existing application ID and signer retained.

| Field result | Download Mbps | Upload Mbps | Idle RTT ms | Download-loaded ms | Upload-loaded ms | Jitter ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 94.6 | 12.5 | 213 | 269 | 244 | 81 |
| 2 | 105 | 46.4 | 161 | 261 | 271 | 16 |

The associated client export is CONNECTED at 30/30 lines, reports 164 ms global
RTT and no top-level error. Both endpoints identify RC10. Upload throughput is
the outstanding performance goal: 100 Mbps on a capable path. Preserve the
observed download performance, low idle RTT, startup and reconnect behavior.

The logs also contain bounded cleanup timeout/retry events without account
failure. This field designation does not erase those diagnostics or the strict
synthetic pause/rolling-throughput misses recorded at build time. Retention
remains conservative; long-running reclamation still needs observation.

Release binaries, source archive, tag target and all original asset hashes are
unchanged. Build-time metadata remains an immutable record of pre-field checks;
this document records the later user confirmation. No further manual mailbox
clean is requested.
