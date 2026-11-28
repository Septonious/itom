# ITOM 1.5.1 RC20 — handover recovery field candidate (UNQUALIFIED)

Parent: UI-restored RC16 58da8299e08b2b8c65ea2d4a3b81ad6d98a5ad5c. Exact locally validated src tree: f10c45e7a565bef5f8233d0398212b8cb7eb1514. RC7 remains the reference. RC18 writer separation and all rejected RC19/RC20 throughput variants are excluded.

Native bearer-generation cancellation and unbounded retry count with bounded backoff repair stale initialization and exhaustion of three retry attempts. No direct fallback, no TCP migration claim. The home transport and mobile pacing/account policy are unchanged from RC16. Strict RC18 qualification logic (50 kbps bursts, P95 below 100 ms, 200 Mbps minimum) is restored with RC20 revision identification. No gate relaxed.

Local Go 1.27.1 offline: qualification worker/provider/fieldgate race tests and seven Python gate tests passed; targeted native rebind race tests passed three repetitions; Linux amd64 built. The final exact source passed native direct and encrypted handover tests with an 8-second outage; fresh authenticated six-echo verification resumed 4.469/4.471 seconds after restoration. Earlier matching encrypted runs: RC16 reference 14.418 seconds, new implementation 4.228/4.224 seconds. This is synthetic, not a physical Android-device test.

Steady cellular 49/4 Mbps test still fails performance objectives. Retained source: 29.61/2.26 Mbps with 7/8 uploads, and 32.52/2.47 Mbps with 8/8 uploads. Faster grant variant completed 8/8 twice but delivered 29.59/2.52 and 29.06/2.26 Mbps, with no consistent throughput or latency improvement; rejected. Desktop batching and light-budget variants also rejected. Full sustained home and cellular matrix not passed; field testing only, not a stable release.
