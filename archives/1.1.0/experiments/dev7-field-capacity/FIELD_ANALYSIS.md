# DEV6 field capture and DEV7 experiments

The 2026-10-23 capture reports DEV6, 30/30 active directions, and zero account reconnects. Screenshots report MULTI 25.7/20.3 Mbps and SINGLE 22.8/24.7 Mbps, with idle RTT 529/523 ms. All 134 retained OPEN_OK responses succeeded. The beginning of startup is outside the rotated trace, so the instant Speedtest failure cannot be assigned a proven cause from this export. Application stream closures are not account disconnects.

Every server account retains substantial earlier-session quota debt: 8,565,977–11,193,823 bytes and 140–173 message slots out of 12 MiB/192. These are still charged and must not be discarded because the client wallet was cleared. Server bulk carrier selection has median 58.8 ms and p95 976.6 ms waits; client median 139.7 ms, p95 942.8 ms. There are 6,925 server control APPENDs and 8,764 client control APPENDs in the retained intervals. Server APPEND DATA median 185.3 ms; cleanup exact FETCH median 197.5 ms, observed deletion median 271.9 ms. These durations include service and local scheduling, and are not raw network RTTs.

Control readers lack independent IDLE sockets at the observed caps (client two/server three sockets per account). The shared fallback used generic SELECT-then-FETCH on every poll. Its counters can include concurrent cleanup on that same physical socket; they must not be interpreted as dedicated-reader SELECT counts.

DEV7 changes under evaluation:

* Use bounded live-tail reads on the shared control/cleanup socket, retaining folder-switch selection and periodic UIDVALIDITY audits.
* Overlap read-only SELECT and exact BODY discovery, withholding results across selection failures or changed incarnation.
* After verified selection, pipeline exact-set STORE and UID EXPUNGE; join both results, preserving all debt on either failure. The no-UIDPLUS fallback remains unchanged.
* Where genuinely remaining capacity fits two full bulk copies per direction plus a complete FAST body, use that capacity before assigning extra bytes to the FAST cushion. Normal debt-free profiles, quota ceilings, durability and historical debt remain unchanged.

Rejected experiments: giving FAST recovery grants half of remaining ordinals (a subsequent loaded probe timed out); overlapping presence SELECT/FETCH (no consistent throughput benefit and more stalled download windows). Neither is part of the candidate.

The new fixture uses all fifteen captured capacity shapes and real TLS/public supervisors with a healthy 20 ms server route, 40 ms client route, 500/1000 Mbps capacities, and capped sockets. It models capacity, not exact original grant records, device CPU, provider processing or a calibrated trace replay. Original 100 Mbps, startup deadlines, and no-stall assertions remain. Existing high-rate empty/less-indebted journal fixtures cannot predict this installed-state performance. All preliminary six-phase sensitivity runs still FAIL the 100 Mbps/no-stall criteria, including RC2. No real-world 100 Mbps restoration or instant-failure fix is claimed.

The first loaded race run also exposed a real diagnostic-snapshot race in admission: another worker could re-plan a waiter while its diagnostic struct was copied after unlocking. DEV7 now copies it under the existing mutex, with mixed fast/bulk concurrent admission coverage. This changes diagnostic ownership, not congestion thresholds or delivery credit. It is distinct from the older race-instrumented 1100 ms loaded-chat timing miss; no build containing an actual data race is eligible for publication.
