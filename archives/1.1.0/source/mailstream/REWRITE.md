# Mailbox stream transport contract — MAILSTREAM-RC5

The first working field release achieved approximately 170 ms idle RTT and up
to 50 Mbps before speedtests failed. RC4 traces then show maintenance pausing on
five accounts, their control cursors freezing, and a subsequent handshake
waiting behind session teardown. The trace does not distinguish a hung extra
LOGIN from a hung first DELETE. The older transaction capture separately shows
UID EXPUNGE timing out after successful APPEND/FETCH/STORE. Neither command is
a sound live delivery prerequisite. The old implementation remains in git and
`../../retired/mailstream-rc4-storage/` for evidence, outside the active modules.

## Session and delivery

Fresh Noise NNpsk0 sessions bind the configured per-client 32-byte PSK and
protocol identity. Startup requires bidirectional encrypted DATA echo and a
mailbox ping. Any healthy account can establish contact; no anchor account,
wallet reconciliation, legacy mailbox scan or deletion gates readiness.

The pinned go-imap client owns response parsing and literal grammar. Each
active endpoint has three sockets per account: control reader, DATA reader and
APPEND writer. There is no extra maintenance login. A standing server identity
keeps its control reader and opens DATA/writer after authenticated admission.
Provider socket budgets include all configured identities. Same-user replacement
joins the old DATA reader before releasing admission; the authenticated silence
lease handles abandoned peers independently of throughput control.

TCP streams have independent offsets and receive windows. Slow application
consumers cannot block every receive stream. Small writes dispatch immediately;
bulk batches only mail already queued. Existing bounded delivery/consumption
windows and throughput controller are retained in RC5. Mailbox receipts do not
release application-consumption credit. Late WINDOW/FIN and resets preserve
stream EOF and byte integrity. Control uses measured NOOP polling; DATA uses
IDLE plus bounded tail repair for missing/coalesced notifications.

## Two-slot reclamation

Each account/session/direction has two fresh DATA folders. Logical generation
increases monotonically and selects physical slot `generation % 2`. A generation
contains exactly 32 bounded MIME containers. At most two generations can be
unreclaimed; the writer cannot reuse a slot while its earlier ownership record
remains. A blocked DATA slot does not prevent control messages, APPEND completion
processing or shutdown, and routing can use another account.

The sole DATA reader validates sequence, MIME size and the exact UID set. Only
when all 32 messages are consumed and the selected mailbox contains exactly
those messages does it STORE Deleted for those exact UIDs. Standard CLOSE then
expunges the fully drained, dedicated slot and unselects it. STATUS must prove
zero messages and unchanged UIDVALIDITY. The reader selects the next slot before
sending authenticated reclamation confirmation over control.

The producer releases the exact generation's retained-byte charge only after
that confirmation, terminal completion of all its APPEND tags and packet
receipts. A BODY may become visible before its APPEND tag; the application test
explicitly delays that final tag. Missing or ambiguous outcomes keep the charge
and prevent slot reuse. No folder DELETE, UID EXPUNGE, broad EXPUNGE command or
maintenance socket participates. CLOSE remains subject to live-provider testing;
a successful loopback server is not evidence that every provider implements it
promptly.

The independent TLS IMAP regression models account-wide locks if DELETE or
UID EXPUNGE is attempted. It requires 36 MiB of random, byte-verified HTTPS in
both directions, physical slot reuse, bounded folder count and an immediate
fresh session. It asserts that neither poison command ran and CLOSE did run.

## Capacity and operations

Retention is durable and bound to physical account identity. Unknown APPEND
outcomes remain charged. Crashed/old sessions are not automatically deleted or
refunded; only aggregate debt survives process death. Discovery logs remain
append-only. These are sustained-use limitations, not solved by slot recycling.

The user-selected policy resets server-local counters on manual stop, restart,
update and IMAP purge attempts, including a failed purge. It does not remove
provider folders, credentials or profiles. Unexpected crashes preserve debt.
Client private state must be kept when updating the signed APK. The manager's
account check tests authentication/NOOP without selecting old DATA folders and
reports stage/reason rather than printing blank fields on context timeouts.

## Validation and limits

Local tests use verified TLS IMAP sockets, real HTTP/TLS bytes, SOCKS and IPv4
TUN native boundaries, cancellation, restarts and provider socket limits. The
qualification runner varies client/provider delay and shared link capacity,
retains failed runs, and measures full-phase verified payload throughput. A
short local 280/313 Mbps result is not a sustained Rambler/mobile measurement.
Targets are 300 ms idle/light RTT, 2000 ms loaded RTT and sustained 100 Mbps
upload/download where path capacity permits. On a 7 Mbps link, integrity,
responsive control and cancellation recovery are the appropriate requirements.

No release is field-qualified until a matched app/server passes actual use.
The direct DNS policy, application theme and persistent signing key are retained.
