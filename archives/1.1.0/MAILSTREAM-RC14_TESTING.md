# ITOM 1.1.0 MAILSTREAM-RC14 — repeated-transfer testing candidate

RC10 remains the first field-ready baseline. RC14 is not field-qualified.
The sustained real-provider 100 Mbps goal remains unverified.

RC13 field testing improved to 118/62.8 Mbps down/up with 154 ms idle RTT, but
the next run fell to 69.4/11.2 Mbps. Nine client accounts approached their physical
retention caps while nine server accounts reported cleanup timeouts. Their mail
carriers stayed connected. The current change targets storage reuse after load.

Control and historical cleanup now inventories at most 256 sequence positions
per turn and mutates only UIDs within the authenticated consumed prefix. Audited
partial proofs advance control credit; historical folders advance only when all
pages prove emptiness. A live unread tail is preserved. Active recovery finishes frozen pre-session
control boundaries rather than chasing a growing live tail. A recovery owner
receives at most two preferred turns before an ordinary FIFO collection turn;
its ready actor is revisited promptly. Idle cleanup still sweeps the full quiet
scope after disconnect. STORE is drained to tagged
completion before EXPUNGE, then fresh SELECT verifies the deletion. No unknown
outcome is refunded. All physical retention and protocol caps remain unchanged.

The existing writer may spend up to eight seconds on an optional bounded
collection turn, independent of NOOP/APPEND timing. Only one mutation lease is
active globally; dispatch excludes its borrowed writer and uses other accounts.
Already accepted control packets are published before optional cleanup may
borrow the actor. Receivers remain independent. User cancellation still interrupts raw IO.
There are no extra connections, folder DELETE, CLOSE or UID EXPUNGE commands.
Reclamation diagnostics distinguish STORE, EXPUNGE and audit stages and record
stage duration, making the next field capture more specific.

Established bulk bursts may coalesce for up to eight milliseconds, scaled by
APPEND completion time, to fill existing bounded containers. Isolated small
writes and priority control bypass this timer. Queue, MIME and flight limits
remain unchanged.

A persisted 51 MiB/account crash reservation reproduces approximately 535 MB
of initial debt. Cold-start transfers and their performance remain recorded;
after production audits clear that debt, another upload/download pair verifies
that bandwidth returns. No fixture modifies a running ledger or grants credit.

Tests cover unread controls, partial historical deletion, slow provider mutations,
early EXPUNGE bursts, repeated transfers with retained controls and per-message
mutation service cost. These costs are explicit stress hypotheses, not a claim
about a recorded Rambler command duration. Prior weak-link, startup, DNS, parser,
retention and Android checks remain required. Every strict target miss is retained.

Install the signed APK in place and update both endpoints. App ID and persistent
signer remain unchanged; version code is 10139. Upload the extracted server .bin
to /tmp and use manager option 18. No manual account cleaning or profile reset
is required. Verified Actions downloads are used because GitHub denied release
creation for RC12; no GitHub release page is claimed without successful creation.
