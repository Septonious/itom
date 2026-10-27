# RC1.8: active FETCH and cleanup overlap before the first DATA echo

The new paired RC1.7 captures distinguish this run from the earlier roster
failure. Both endpoints agree on revision and configuration binding. The phone
selects all 20 lines, authenticates the short request, completes Noise and
roster, starts runtime and obtains the initial duplex permissions. Its first
probe stream opens and buffers 16 DATA bytes; no echo is received before the
unchanged probe deadline. The app correctly reports V2_STARTUP_PATH and does
not establish a TUN or report READY.

Server events in UTC:

| Event | Time |
| --- | --- |
| Waiting for request | 18:03:29.153 |
| Invitation | 18:03:31.547 |
| Noise | 18:03:32.761 |
| Roster | 18:03:34.479 |
| Runtime | 18:03:35.853 |
| Server permissions ready | 18:03:35.856 |
| Runtime snapshot, all sockets open | 18:03:37.858 |
| Runtime failure | 18:03:38.512 |
| Client DATA echo failure | 18:03:38.573 |

The first socket failure is a03 FAST with `peer_eof`. Its BODY fetch counters
are 31 started, 30 completed, one failed; UID EXPUNGE counters are four started,
three completed, one failed. All eight APPENDs complete, as do all FLAGS and
STORE commands. Other carriers retain `owner_closed`, showing the subsequent
endpoint-wide abort rather than twenty independent provider failures. This is
an active-session failure, not the modeled idle-expiration failure from RC1.7.

Before the failure, another fast socket has BODY, APPEND and EXPUNGE outstanding
for around 1.6 seconds despite much shorter normal IMAP service. This supports
examining command concurrency, but is not a provider error response. The logs do
not identify a provider limit, internal server error or the exact reason for EOF.
Client inventory activity is also high; its effects remain unqualified rather
than being used to justify a speculative timeout increase or reconnect loop.

Inspection found that the shared executor queues BODY/FLAGS/inventory work but
admits UID EXPUNGE independently. Therefore deletion can cross an unfinished
FETCH tag and a subsequent FETCH can cross the deletion tag. A provider must
coordinate sequence-changing EXPUNGE responses with outstanding read commands.
The client need not require the provider to support that avoidable overlap.

The new actual-wire regression holds a FETCH tag and observes whether EXPUNGE
arrives prematurely. RC1.7 sends it immediately. The modeled provider closes
the connection on this overlap, matching the field's failed-command/EOF class.
After the fix, both sides of the barrier remain intact across cancellation of
a waiter. APPEND is independently admitted during the delayed EXPUNGE, so the
correction does not serialize all data publication behind metadata cleanup.

The implementation reuses the existing bounded FIFO turn ownership. A tag owns
its turn until tagged completion or the original socket watchdog; application
cancellation cannot abandon an issued deletion and admit later reads. Physical
absence, original accounting records and MAC validation remain separate from
command scheduling. No unknown publication is retried or refunded.

The older RC1.5 server stop also had generic carrier failure during outstanding
FETCH/EXPUNGE commands. Its old diagnostics cannot prove EOF, so that remains
supporting history rather than confirmation of the same provider defect.

Fixed command-class close diagnostics will preserve the exact admitted command
combination if a future EOF differs from this reproduction. Raw field archives
remain private and are excluded from release evidence. Synthetic evidence keeps
the before-fix failure, the intermediate test spelling error, final corrected
results and all observed qualification failures.
