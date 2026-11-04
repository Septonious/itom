# RC6 control retention and RC7 recovery audit

The user observed 512 and 883 messages left in control folders after stopping
sessions and the server, and manually cleaned the accounts. RC6 retained its
discovery/control log forever. Its local stop/reset policy did not mutate mail
at the provider. Old partial DATA and historical aggregate capacity also lacked
automatic reconciliation after an interrupted session.

RC7 reads control by UID rather than shifting sequence position. Authenticated
consumption watermarks permit verified prefix collection using the server's
existing APPEND actor. Tagged APPENDUID results associate exact charged bytes
with that proof. Incoming and outgoing control turns alternate. DATA delivery
readers retain RC6's independent parking and fresh-slot behavior.

Inventory is limited to the configured identity's exact recognized namespace.
Old partial DATA, parking and earlier control folders are emptied and audited.
Historical reservation debt is released only after this recovery completes;
both full client and full server ledgers are covered. A separate durable 64 KiB
bootstrap/recovery allowance prevents ordinary 64 MiB saturation from blocking
recovery itself. Application traffic cannot use it.

An unknown collection outcome is quarantined to its owner and folder. Backoff
retries audit it before the global collector resumes. A closed endpoint cannot
permanently retain ownership of that circuit. No automatic DELETE or UID
EXPUNGE is used, even at idle: the earlier provider account-lock evidence makes
folder deletion an unsafe prerequisite for the next connection. Empty folder
containers remain. When all sessions are idle, one interruptible bounded login
empties control tails and checks abandoned mail. Admission preempts this login.
The server must be running for maintenance; stopping it immediately defers work
until it starts again.

Intermediate regressions are retained in the release evidence. A proposed
128-message admission window blocked receipts on a client whose server's cleanup
had failed. It was removed; ordinary byte and actor-queue bounds remain. A burst
also exposed incoming-control cleanup starving the reverse direction; alternating
eligible directions fixed it. The regression now checks that both directions
drain after an application burst, rather than treating a transient peak message
count as a reason to block TLS acknowledgements. An old test that prohibited all
historical refunds now requires verified recovery proof before allowing one.

Qualification includes real loopback TLS IMAP and verified HTTPS requests,
24 MiB download/upload/reconnect with provider mutation failures, 180 small
HTTPS requests during control collection, two-way full-ledger recovery, unrelated
mail exclusion, idle-tail cleanup, reconnect and lost-EXPUNGE-result recovery.
Independent one/four/32-stream and 7 Mbps model cohorts retain every target flag.
Source, normal/race/native/SDK/vet, downloaded server startup/restart and Android
build/tests/lint evidence accompany the final release. These checks do not
qualify live Rambler, every delayed provider commit, or Android/mobile behavior.
Permanent provider mutation failure can still exhaust finite retained capacity.

Further long-run checks exposed control maintenance losing an account FIFO turn
to repeated DATA collection. A requested maintenance operation now keeps that
turn, and old-debt recovery completes once its fixed boundaries are covered,
independently of ongoing new traffic. Small verified refunds batch durable
reservation writes in 512 KiB quanta while retaining a safe high-water bound.
Control reads use an absolute forward UID range, crossing holes without STATUS
on a selected mailbox. A frozen RC6 reference also reproduced a local loaded
ping timeout; its evidence is retained alongside candidate failures and final
hosted/local qualification. No timeout is relaxed to make those results pass.

The faster control path also exposed a pre-existing controller coupling: light
DATA probes trained the bulk rate/minimum RTT across application-limited gaps,
while delay-gated exploration could not grow the next full-container transfer.
Bulk estimation now excludes interactive DATA, restarts its observation after
idle gaps, and grows from measured delivered rate. Clustered byte-ACK growth
was rejected after a 7 Mbps timeout; measured-rate flight headroom is bounded.
The bounded variant completes the 7 Mbps download cancellation and new HTTPS
check with zero failed accounts; final repeated-transfer qualification follows.

Final source selected for publication: `eb16f16a6f19257964977ce28881b89d90af4384`.
Control pressure now measures consumed incoming and owned outgoing MIME bytes,
not total DATA debt, so DATA capacity pressure does not hand its GC turns back
to control tails. Bulk headroom uses a finite half second of measured delivery,
independently of the faster probe RTT. Hosted final-source gates pass. The
publication evidence records prior local failures and interrupted work separately.
