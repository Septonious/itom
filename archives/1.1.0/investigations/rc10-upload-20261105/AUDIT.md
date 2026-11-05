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
horizon is one second. Server download control retains RC10 behavior.

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
