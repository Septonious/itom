ITOM 1.0.0 RC10.1 — diagnostic field-test build

Baseline: packaged RC10 (bfde125). Transport policy, wire protocol, quota, journals, recovery and org.itom.client identity are preserved.

Adds bounded latency profiling to the existing diagnostic exports. See ../audit/PROFILER.md. Update client and server for complete endpoint coverage. No RTT or throughput improvement is claimed by this instrumentation-only build; RC10 unresolved qualification limits remain.
