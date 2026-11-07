# FIELD29 — development snapshot, field acceptance pending

## Runtime changes

FIELD28's bounded UID revisits, authenticated durable-prefix replay and pre-bootstrap reconciliation are retained. The new restart path uses retained bbolt ledger/wallet state and a fresh authenticated session. Unregistered old DATA discovered during startup is retained within existing 128-message/12 MiB bounds. Old authenticated Usage reports match that retained BODY or enqueue bounded discovery behind the live cursor. Cleanup reads at most one recovery page per turn. Empty or temporarily invisible pages never prove deletion or refund quota.

Already registered old attempts are also queued from the durable server journal after the fresh handshake. This matters when a wallet legitimately pruned its descriptor after Registered ACK: recovery must not depend on another wallet replay. Exact folder, UIDVALIDITY, UID and digest checks and physical deletion proof still precede debt release. Unused old allowance waits for the final peer fence. Reclaimed/retired old duplicate DATA is idempotent, without plaintext delivery, extra refund or old-wallet writes. No DATA retry, budget reset, additional DATA worker or forced LITERAL+ extension is added.

Four restart variants cover startup-visible, late-visible, later-page and registered-wallet-ACKed DATA. Restoring FIELD28's old runtime fails the first three new variants: missing remote descriptor or stranded debt. This is retained as a negative regression reproduction, not a failed final gate.

## Corrected test scope

The application scenario runs actual HTTP/2 over TLS through ITOM supervisors and a local TLS/IMAP fixture, with simultaneous upload/download and eight fresh HTTP/1.1 short requests. It validates exact upload SHA/payload and download bytes, observes a short request completing while bulk is in flight, preserves logical session/generation and carrier epochs, and enforces ten provider sessions/account with 100ms mailbox visibility delay. It does not traverse Android VPN or a public Speedtest/provider. Successful local integrity and liveness cannot establish field bandwidth or RTT.

The opt-in synthetic throughput harness explicitly records `field_qualified=false`. Its temporary 100 goal requires minimum verified duplex/tail rate >=100 Mbps, no speed tolerance to substitute 90 Mbps, and every zero-loss RTT strictly <1 second. Non-mobile profiles also use the temporary bound rather than the historical <800ms tier. Historical adaptive RTT and 125 Mbps calculations remain diagnostic/historical checks. No more-than-half account outage is introduced. Loss qualification is deferred.

## Consolidated scenarios

160 resource-independent test functions become 33 coherent file scenarios, reducing standalone entries by 127. Eleven pure boundary families stop generating individual subtest records; all boundary assertions remain. Lifecycle/concurrency tests, Cleanup/TempDir/Setenv/Name-dependent checks and fuzz seeds retain their original scheduling. Mapping is in TEST_CONSOLIDATION.json. This reduces report fragmentation, not the amount of security validation or a claim of runtime savings.

Before consolidation: 621 PASS results in the prior complete normal/race runs. New recovery case, HTTP scenario and temporary RTT check are additions. The consolidated full normal/race gates report 395 PASS results: 270 top-level Test/Fuzz functions and 125 subcase/seed results, plus three explicit opt-in skips. Final exact-source launcher results are recorded separately in the report/evidence.

## Exploratory measurements, not field qualification

Ten accounts, four streams, baseline 250ms, cap600Mbps, zero loss, warmup10s, intended60s. These binaries predate the final registered-wallet recovery and test consolidation changes; no qualification or paired regression median is inferred.

- LITERAL+ main candidate: complete60s; upload117.161/download137.197 Mbps, tail118.349/136.454; 60 probes, zero timeout/missed, RTT median578ms/max784.739ms; ten carriers, zero spontaneous exits. FAILED under the then-active adaptive RTT criterion. The newer temporary threshold is not retroactive acceptance of the final source or field behavior.
- Synchronizing literals main candidate: interrupted after24s by a1s probe timeout; upload53.749/download98.597 Mbps observed, not sustained. FAIL. The100 Mbps target is not established on this path.
- Rejected global fast-slot reserve prototype: complete60s,118.698/137.393 Mbps, RTT median553ms/max853.942ms. FAILED under then-active adaptive RTT. Slight mean improvement with worse max RTT and no paired sustained qualification is insufficient; prototype is NOT integrated.

The captured field TIMEOUT remains unresolved without a new paired client/server trace. We have evidence for missed metadata and stranded old accounting, not proof that every field issue is fixed.

## Release decision

NOT RELEASED. A complete normal Go gate is mandatory, race/integrity checks accompany it, but field acceptance still requires corrected application/provider measurements. Next field validation must measure each direction separately (no aggregate sum), sustained and tail windows, every probe including timeouts, fresh short requests during load, provider command/literal capability, physical connections, carrier exits/recovery and persistent journal state. At least three paired sustained runs per relevant profile are needed for the agreed regression comparison; >10% median degradation vetoes a candidate. Synthetic PASS alone cannot approve deployment. Android compilation/device, Windows execution and public provider Speedtest were not performed here.

## Final build/integrity checks

The stock server launcher on the final source passed mod tidy/verify, pinned module/fork checks, the full normal Go gate (395 PASS,0 FAIL,3 opt-in SKIP,18 packages), IMAP fork normal tests, vet and Linux amd64/arm64 builds. The tunnel package completed in58.972s within the launcher's unchanged120s timeout.64 Python/source checks passed.

The full core race gate passed395/0/3 across18 packages (tunnel94.090s). It covers the final runtime and acceptance functions; the HTTP test's last overlap/HTTP1/peak-report refinements were rerun separately with -race on the final source and passed. Native normal/race and IMAP fork race passed. Windows tunnel cross-compilation passed; it is not Windows execution. Fuzz coverage is the34 seed records of4 targets, not a timed fuzz campaign.

Final normal HTTP scenario:4,718,592 verified upload bytes and4,718,592 download bytes;8/8 short HTTP requests completed during bulk; provider peak10 sessions/account at cap10;0 connection-limit refusals. Cap exhaustion/retry behavior is therefore NOT qualified by this particular run. All four carriers and the logical session were preserved.
