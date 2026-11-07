# FIELD42: startup regression repair

## Evidence

New client captures contain two field20 runs (starting 2026-10-07 06:42:02Z and 06:43:32Z). The server has three field20 runs from 06:41:00Z. Only those runs were used; the same rotating archives also contain field17/field19 history.

Client: 90 recovery_page IMAP_SCOPE failures; server: 122. Every one has detail IMAP_SCOPE_FETCH_MISSING_BODY. Both sides cover all 10 accounts. Authentication succeeds before these failures. Current runs have zero frame_rx/packet_rx/packet_tx_attempt: the failure precedes application load and handshake traffic. Old performance measurements are irrelevant to this startup failure. No credentials or raw mailbox payloads are copied into this report.

The compact trace does not contain wire responses. Source inspection found that the BINARY decoder omitted readPartialOffset, although the application requests a partial BINARY fetch. The raw peer with SIZE, HEADER and BINARY[1]<0> reproduces exactly IMAP_SCOPE_FETCH_MISSING_BODY on unmodified FIELD41; the fixed parser accepts that response. This is a proven code defect and a sufficient cause consistent with field evidence, not a claimed packet capture from Rambler.

RFC 3516 section 4.3 permits an origin after BINARY[]. The old local in-memory server omitted it even when the command requested a partial section, explaining why previous compliant round-trip tests missed the defect.

## Changes

1. Decode BINARY origin and preserve it in the returned section. Match the requested part and origin; zero-origin whole-section responses remain compatible. Track required BINARY independently of HEADER for UID completion.
2. Use the ordinary physical BODY path for recovery, inventory and exact checks. BINARY is restricted to live streaming calls with a callback.
3. After a tagged-successful incomplete BINARY representation, drain the bounded FETCH then disable BINARY on that connection and retry the read once with BODY. NIL/empty decoded bodies cannot become accepted payload evidence. Fallback does not repeat APPEND, advance an incomplete cursor, refund quota or reset journals. Invalid UID/part/origin/size/oversize still fail closed.
4. Preserve the FIELD40 transport engine, pacing, grants, logical ownership/rekey and FIELD41 early complete-row streaming/TCP diagnostics. No Android UI or native TCP-copy behavior changes.

## Focused verification and limits

See FIELD42_RESULTS.md and ITOM_FIELD42_TEST_LOGS.zip for exact executed checks. New raw-peer cases cover header/body ordering, partial origin, no origin, missing components, NIL/empty body, wrong origin/part and recovery BODY. Existing real TLS MIME/deletion evidence checks cover BINARY success, provider NO and altered-header fallback; each now checks recovery BODY first. Startup of 10 legacy-profile accounts advertises BINARY; both grant classes must be ready and actual TCP echo succeeds on every lane.

The full historical Go suite is not rerun. No fresh throughput benchmark, external provider login, Windows execution, Gradle/AAR/APK build, Android lint/unit/device qualification is claimed. The launcher all command still performs its full gates. FIELD40 remains the only user-confirmed working field baseline; FIELD42 needs field verification on the updated server and APK.
