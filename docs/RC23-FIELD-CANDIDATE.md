# ITOM 1.5.1 RC23 — controlled field test, not qualified

Exact locally tested src tree (excluding generated Python bytecode): 16bfc232fdad95410288757968ef60acbd7503d0. Counter-based final source selected instead of the later experimental guard/emergency repair variant, which has incomplete mobile evidence. No merge into main or stable release. User explicitly requested a field build despite outstanding performance qualification.

Changes: 3–10 account shared profiles, minimum 3 accounts, sequential balanced grant with admission-order remainder, adaptive borrowing/reclaim using unique application bytes rather than IMAP control bytes, multi-signal cellular path adaptation, RC23 socket-bound encrypted recovery, authenticated bearer confirmation, relay credit and RC20 handover fix. Physical failures can prevent three usable paths; emergency sharing experiment not included.

Local source: full core and native race suites, qualifier Go and 13 Python tests passed. Direct/encrypted 55s blackout recovered 32.12/1.80 and 27.05/2.13 Mbps, 8/8 in each direction and 6/6 probes, P95 loaded 708/854 ms. Handover 5.203/5.191 s versus RC20 ~4.47 s. 82 samples above or equal to three usable paths. Home gates failed latency/ramp and fixed-grant compatibility; direct DL/UL 269.3/298.7 Mbps. No gate relaxed. Linux amd64 built locally. Final-source full mailstream race sweep and sustained mobile repeat matrix incomplete. Earlier 147/147 and eight blackout passes refer to the previous socket-bound source, not this exact tree.

Field testing only. Use matching APK/server/exit builds. Keep RC20 rollback. CI includes regression checks and separate strict mobile qualification; a successful package build is not promotion.
