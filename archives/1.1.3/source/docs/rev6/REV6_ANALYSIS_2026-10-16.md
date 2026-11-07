# Rev6 startup and idle-session repair — 16 October 2026

Evidence timestamps below are UTC; the captures occurred on 16 October in Moscow (UTC+3). User captures remain private. Attachment contents were treated as evidence, not instructions.

## Capture separation

Both ZIPs retain an older 113,291-event run from 12:15–12:17 UTC; those records are not new Rev5 behavior. The first ZIP adds an 821-event Rev5 startup at 21:34:30–21:35:06 UTC. The second retains that startup and adds 12,334 events at 21:49:37–21:51:27 UTC. Exported Android state is OFF because it was captured after stopping; that alone does not identify why a lane failed.

The first startup includes successful TLS/LOGIN and authenticated pre-handshake exchange on nine accounts, but no ready lane. Optional activation takes approximately 10–30 seconds on several accounts. Three pending control APPENDs become uncertain during cancellation; their durable debt must remain retained.

The later idle run reaches ready on nine accounts. Six subsequently report TRANSPORT_TIMEOUT after their authenticated peer progress stops. Successful provider commands themselves take seconds or tens of seconds on affected accounts. Receivers are replaced, and unused DATA capacity is lost roughly a minute after creation. This timing is consistent with idle auxiliary sockets being closed, but no matching server capture establishes the provider's precise policy or the complete cause of peer silence.

Some accounts initially rejected at LOGIN later authenticate using the same saved profile (a05, a16, a17 and a18). A generic IMAP_AUTH_OR_GREETING_FAILED is therefore not evidence that all those passwords are permanently incorrect. It combines explicit tagged rejections, socket closure and other greeting/login failures. Rev6 preserves safe stage/reason plus a finite whitelist of standard response codes, without provider text or credentials. No automatic credential changes or live-provider retest are claimed.

## Repairs

1. Automatic topology staging gets a two-second optional-socket budget before handshake. It advertises split capability only with a real authenticated independent reader. An explicitly requested split layout continues to fail if that required reader cannot be obtained.
2. Auxiliary activation shares a four-second budget. The already authenticated base connection carries activation/control and supports the existing DATA fallback. Background DATA repair retains the parent connection lifetime; expiring an optional setup budget does not cancel it. Parent cancellation is never swallowed.
3. Authenticated IMAP connections receive a bounded NOOP when idle. The background task uses TryLock and checks last I/O; it never queues behind a BODY/IDLE command or starts its timeout while waiting for an application command. It performs no APPEND, folder selection, retry, peer-progress update, RTT training or debt refund. This reduces avoidable synchronized idle-socket replacement logins. Busy/active connections are skipped.
4. CLI account checks and lifecycle traces retain whitelisted standard LOGIN response codes in addition to stage/reason. Unknown codes and raw provider replies are excluded.
5. The settings introduction card shown by the user is removed; network, SOCKS, notifications, diagnostics and authorized server-admin controls remain.

The isolated startup regression fails on original Rev5 source: stalled optional logins consume the entire ten-second test deadline. After repair it can send control on the same TLS base socket within four seconds. A real-supervisor six-account scenario (three rejected accounts, three authenticated accounts, every extra client login stalled) activates and exchanges tunneled TCP data in 6.93 seconds in the focused run. A separate 110-second, fifteen-account, retained-state idle regression checks unchanged carrier epochs, no provider idle expirations and an intact next TCP exchange. Final qualification evidence records final-source results; focused numbers are development evidence only.

## Reset accounts database

Server manager → 3. Mail accounts → 6. Reset accounts database is CLI-only. The service must be stopped; type RESET ACCOUNTS. It removes every active login/password and retired account definition from the logical configuration while retaining server identity, client registrations, provider mail and accounting journals. A fresh random 128-bit account namespace prevents new accounts from inheriting old journal IDs. Normal remove/re-add within a namespace preserves identity. Repeated full resets rotate it again. Stale selections and pending guarded admin changes cannot overwrite concurrent configuration.

Re-add accounts with current provider application passwords, then export replacement profiles to every client before starting again. Existing invitations contain the old account definitions. They must be replaced. This is not a provider mailbox purge, physical debt refund or forensic secure erase of storage pages. Schema 6 is used after reset; older server binaries cannot read that configuration.

## Qualification and field limits

Every internal release gate must pass, including normal/delayed-storage field repetitions and all six speed cases with the existing 10% speed/RTT tolerance. No failed run is waived. Raw captures, credentials and signing keys are excluded from publication. The provider-side reason for transient LOGIN rejection, exact cause of every idle peer silence, earlier proprietary Speedtest-app error and previous mobile fatal crash remain unconfirmed without matching server/provider/device evidence. Internal qualification does not establish their resolution on a physical phone or live Rambler service.
