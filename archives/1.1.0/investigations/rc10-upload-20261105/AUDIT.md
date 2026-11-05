# Upload investigation from the first field-ready RC

RC10 is the preserved field baseline. The user reports 94.6/12.5 and 105/46.4
Mbps download/upload, 161–213 ms idle RTT, and successful functional testing.
The supplied client export is connected at 30/30 roles, with 164 ms global RTT.
The sanitized field fixture contains global upload/window/receipt samples only;
no account names, profile keys, addresses or raw archives are committed.

During both upload phases, sent DATA repeatedly fills its flight allowance. The
faster run grows from about 7 to 59 transport Mbps over its upload phase. The
slower run remains around 8–18 Mbps. Bulk receipts take roughly 330–650 ms even
though direct IMAP latency is tens of milliseconds. The fixed half-second rate
horizon can reinforce a low allowance when receipt RTT approaches that horizon.
This is a demonstrated limiting mechanism, not proof that every upload cost is
explained by the controller. Cleanup timeout/retry events also remain visible.

An independent FIFO-link regression reproduces a 100 Mbps capable path limited
to 8.5 Mbps by half-second receipts. A real TLS IMAP model delays server DATA-body
FETCH by 350 ms while leaving NOOP, control and client download reads independent.
Frozen RC10 completes two uploads at 6.22/6.26 Mbps while downloads exceed 300 Mbps.
This is a provider stress hypothesis; 350 ms is not a measured provider constant.

The upload prototype retains the initial 512 KiB grant, finite memory/retention,
control reserve, DNS and cleanup ownership rules. Only client outbound bulk can
use bounded extra measured-rate headroom when actual queued DATA exhausts its
flight allowance. Direct/control probes cannot train that bulk RTT. The maximum
horizon is 1.25 seconds. Server download control retains RC10 behavior.

Server upload readers request at most 32 already-available containers instead
of eight, streaming each bounded body; no timer waits for a fuller batch.
Client download readers retain eight. This amortizes slow body-FETCH service
without changing roles, mailbox ownership or the 128-container generation.
A smoothed bulk receipt RTT is added to diagnostics.

Short, identical delayed-read prototypes progress from 19.7/31.2 Mbps, then
59.9/58.5 Mbps, to 80.1/92.5 Mbps upload with the upload-only 32-container reader.
A larger-headroom intermediate still averaged only 67 Mbps and was rejected.
These are prototypes, not field qualification or a claim of sustained 100 Mbps.
Longer repeated transfers, constrained-link latency and the established RC10
regression gates are required before packaging a successor.

The 30-second delayed-read prototype reached 115.1/113.2 Mbps uploads, but its
weak-link test failed a loaded ping. It was not packaged. Compressed receipts
inflated a short ACK-arrival rate estimate and admitted several MiB of flight
on a 7 Mbps shared link. The corrected prototype records delivered-byte and
sending/delivery timestamps with each first publication, caps the rate sample
using the larger of its send and delivery intervals, and counts each reliable
DATA acknowledgement once. Application-idle time is excluded from fresh bursts;
RESET removes its bulk-flight charge. This estimator and additional headroom
apply only to client outbound DATA. The server's download controller remains
at RC10 behavior.

The corrected 7 Mbps whole-path model completes at 4.72/4.39 Mbps upload/download,
with 101 ms maximum idle/post RTT and 1709 ms loaded RTT, no failed accounts,
and no flight or stream credit left after cancellation and fresh HTTPS recovery.
An independent compressed-receipt FIFO test also keeps the slow flight bounded.
The longer delayed-read model is repeated against this exact implementation
before any upload-target qualification is claimed.

Final exact-source candidate `e3c61864048be2e9a7e4b328b5324a2d91f88d4b` passes required hosted/source/Android checks, three-cycle account-interference and two delayed-FETCH upload acceptance, six server startup cycles and manual-stop capacity reset verification. RC10 remains the first field-ready baseline. Client upload samples resist compressed ACKs; download control remains at RC10. Field cleanup timeout observations are not declared fixed. Unknown outcomes and deferred historical debt remain charged. Remaining strict target misses are retained in publication metadata.
