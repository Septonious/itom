# Source24: нормальная сеть без потерь; кандидат, НЕ РЕЛИЗ

По уточнению пользователя 2026-10-04 квалификация потерь отложена. Scope:100–300ms baseline,50–600Mbps клиентский wire cap,0%loss,5–10healthy accounts. Zero-loss thresholds,10% tolerance,hard RTT1000ms and same5s bandwidth formula unchanged. Goal125Mbps per direction (112.5 tolerated) at sufficient network capacity. Packet loss remains unqualified;200Mbps deferred.

Source23 rejected: minute seed1/B200/C600/0loss mean136.839+Mbps per direction, two RTT violations,max754.238311ms. Diagnostic trace timed out atprobe31 and showed S2C APPEND start bursts equivalent to305.901Mbps per100ms despite150Mbps pacing. Failed logs retained; no rerun erases the failure.

Move S2C bulk pacing reservation from before grant selection to after successful journal grant/preparation, before physical-slot admission. Delayed grants cannot bank simultaneous starts; the paced batch holds no physical slot. Cancellation cancels Prepared and closes its unused grant before returning NotCommitted; Unknown APPEND debt and physical GC proof unchanged. Fast DATA bypasses pacing; ceiling150Mbps and feedback unchanged. Two regression tests cover unadmitted traffic clock, prepared cancellation debt and fast physical availability.

Full Go/core/race/fork/native/build, fixed normal-network matrix and paired field4 regression are mandatory before release. No more-than-half outage profiles. Windows actual execution,Android/live-provider/device qualification must not be inferred from Linux or cross-compilation.
