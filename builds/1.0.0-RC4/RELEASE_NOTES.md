# ITOM 1.0.0 RC4 — pressure-aware response polling

RC3 field results improved latency but regressed speed, with a failed run and physical socket repair history. RC4 restores RC2 default hot pacing during bulk and parallel setup. The 20 ms cap is limited to two quiet reserved readers with direct response hints and light demand; shared empty-streak/quiet-transition history is preserved. Bulk limits, cryptographic/physical accounting, connection counts and recovery authority remain unchanged.

The latest RC3 traces show median echo RTT 285.81 ms and minimum 176.07 ms. Previous-generation snapshots show upstream EOF across original sockets, plus one receive no-progress timeout. The initial loss wave and failed Speedtest are outside the retained trace window, so the cause of remote closures is not proven.

Focused race checks, normal MULTI/pause/blackhole/EOF/post-load recovery, Android tests/lint, native checks and source/build identity checks pass; exact scope is in VALIDATION.json and TEST_EVIDENCE.zip. Finite local transfers do not certify restored provider speed or prevent future upstream socket loss. RC4 is an unstable testing prerelease, with real-provider and full stable qualification outstanding. The historical RC2 startup race limit remains unresolved.

Update both endpoints, preserving profiles/accounts/journals. APK code 10003 keeps the existing app ID and signer. Includes APK, Linux AMD64/ARM64 binaries, complete source, handoff and evidence. Actions verifies and publishes the exact locally compiled artifacts.
