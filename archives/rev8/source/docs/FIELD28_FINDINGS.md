# Field28 work in progress — not a release

The user's SOURCE27 field capture reports 35–40 / 1–7 Mbps and loaded RTT 700–2000ms at baseline ~250ms, at most 16/20 directional lines. SOURCE27 changed test acceptance only; its runtime equals SOURCE24/26. The synthetic historical 125Mbps results do not establish this provider's behavior.

Inputs: ITOM_diagnostics_full(20261004-212843).zip and ITOM_server_20261004T212629.003928425Z(1).zip. Current client run 1791148916174348281, 21:21:56–21:24:26 UTC, 2026-10-04. Old runs and disconnects after user stop are excluded.

Confirmed: a02 COORDINATOR_ACK SHA e394ae82c390deade5cd3f6654c3bf71d3a6eee9dfe8a6dd0ca91908dacdfb20; live reader missed UID3661 while processing UID3664. Server recovery later found the ACK. a10 UsageReport ordinal10 SHA440e5542b73f7caf48521f0779367aea3e0113c9a95fcf37f0ccf10adf7427f4, UID5112, was skipped live but found by recovery. Ordinal11 caused JOURNAL_REMOTE_COUNTER and carrier failure. a05 TIMEOUT remains unresolved; it is not established to be a watchdog or provider rate limit. a02/a06 had zero issued DATA grants and headroom waits; the underlying retained-debt state is not available from the trace alone.

Implemented: bounded exact-UID revisits after passing allocated holes (1024 metadata entries/folder, 30s discovery lifetime, up to 16 revisited UIDs, 250ms cadence, combined with the fresh tail in ONE FETCH command; hot batches revisit at most4); resets on UIDVALIDITY change and retains original tagged-completion/cursor and GC-proof requirements. Gaps exceeding bounds remain a limitation, not proof of deletion. Authenticated forward descriptor gaps request wallet durable-prefix replay; journal contiguity, physical debt and one-shot DATA APPEND remain strict. Repeatable scoped old-wallet controls may precede coordinator ACK without tearing down bootstrap; reconciliation does not require a fresh grant, which could deadlock a full old journal. Diagnostic error classification now distinguishes coordinator and journal counter failures.

New regression checks: TLS IMAP delayed UID visibility behind cursor, metadata/incarnation bounds, journal gap repair without refund, durable wallet replay, pre-bootstrap metadata ordering. A real StartClient/Serve supervisor test with 400ms delayed visibility completed same-socket payload and repeated echoes with zero spontaneous carrier exits. Restoring SOURCE27 cursor logic makes the new late-visibility test fail.

Intermediate full Go: 615 normal and 615 race PASS/18 packages; these runs preceded the last fixture/bootstrap refinements and are not the final exact-source gate. Final checks pending.

Paired baseline SOURCE27/current candidate, three repeats, 10 accounts, 4 streams, mobile RTT250ms, cap600Mbps, zero loss, synchronizing literals, warmup10s, planned30s, hard probe deadline1s: ALL SIX FAIL on probe timeout within1–7s. Ten carriers stay healthy; no spontaneous exits. These short observations are not sustained rates and cannot support a regression median or release.

Rejected experiment: optional third DATA worker and fast-slot reserve failed the first1s probe at baseline250ms. It remains an isolated experimental tree and is NOT included. A high-latency synchronizing APPEND consumes an extra protocol roundtrip. LITERAL+ support must be advertised by provider, never forced. Defaults and numeric release criteria are unchanged. Windows/Android/live-provider execution is not performed here. No release/install recommendation.

## Final combined-reader exploratory load results

Same10accounts/4streams, baseline~251ms, cap600Mbps, 0loss. Advertised LITERAL+: complete60s, verified118.828Mbps upload/135.260download;60scheduled probes, no timeouts, no missed probes,10healthy carriers, zero spontaneous exits; RTT median598ms, p95712ms, max759.551ms,2 RTT-window violations. Speed target125 with10% tolerance is met in this single exploratory run; whole test remains FAIL due to RTT. This is not paired regression/qualification.

Synchronizing literals, strict1s probe deadline: timeout at probe4, only4s measured;55.374/96.724Mbps observed, not sustained. Diagnostic3s cadence/deadline allows full30s measurement while KEEPING the same speed/RTT acceptance:61.148/97.541Mbps,10probes,0timeouts, RTT median588ms/max1005.797ms,4 violations, FAIL. Diagnostic deadline was not an acceptance relaxation.

Added safe append-capability event (synchronizing/literal-minus/literal-plus) and explicit logical-owner/pump failure origins to distinguish the unresolved a05 TIMEOUT on the next field capture. Capability is advertised by the authenticated IMAP connection; no protocol extension is forced.

## Exact final source checks

Full core Go normal and race:617PASS,0FAIL,18packages each;3opt-in tests SKIP each. New delayed visibility supervisor test executes in both normal and race gates. Native normal/race, patched IMAP client race, vet,64Python tests, source checks PASS. Linux amd64/arm64 binaries built with the launcher's -buildvcs=false flag. Initial manual builds without that flag failed on this container's parent VCS metadata; original failure logs retained. Windows tunnel cross-compilation PASS, not execution.

Exploratory load binaries predate the final diagnostic-origin logging edit; they test the same combined-reader/accounting/data-path algorithms but do not qualify the final ZIP. The full final Go gate tests the final runtime code. Packet-loss acceptance, Android APK/device, Windows execution, real-provider rerun, paired sustained regression matrix remain unqualified. Next work: synchronizing-literal latency/QoS and quota-starved restart fixture using retained state; do not alter numeric acceptance to manufacture a release.
