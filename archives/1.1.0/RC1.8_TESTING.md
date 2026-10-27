# ITOM 1.1.0 RC1.8 — fence UID EXPUNGE against outstanding reads

This experimental build addresses the RC1.7 report of V2_STARTUP_PATH after
successful login and roster negotiation. The paired exports show all 20 lines
selected in 904 ms, runtime at 5,950 ms, permission complete at 7,135 ms and the
first DATA echo failing at 9,509 ms. On the server, a03 FAST closes with peer EOF
while BODY FETCH and UID EXPUNGE tags are outstanding. The other sockets close
after the endpoint owner aborts the session. The exact provider-side reason for
the EOF is not present in the logs.

RC1.8 puts UID EXPUNGE through the executor's existing FIFO read-admission
barrier. Earlier BODY, FLAGS and inventory tags must drain first. Later reads
wait for the EXPUNGE completion tag, including when its application waiter
cancels. Ordinary APPEND publication remains concurrent. There is no extra
connection, reconnect, resend, deadline extension or new quota allowance.

The new wire regression rejects a deletion command while FETCH is outstanding.
It fails on RC1.7, then passes with the barrier. It also checks that later reads
wait for EXPUNGE, a detached waiter cannot drop the fence, and APPEND can still
proceed. This is a compatibility reproduction of the observed overlap, not
proof that Rambler closes every such exchange. The exact remote EOF cause still
requires field confirmation.

Close diagnostics now retain the fixed command classes that were admitted when
the first socket failure occurred. They contain no tags, UIDs, provider text,
targets or payloads. Existing first-close reasons and rotating stage snapshots
remain available after cleanup.

READY still requires a successful authenticated DATA echo. All original
application, TLS, OPEN, IMAP and startup deadlines remain intact. Cleanup still
requires the original authenticated BODY, exact UID scope, tagged commands and
independent absence proof; EXPUNGE completion alone releases no debt. Historical
wallet/journal state is retained.

Update BOTH endpoints in place. Run the newly downloaded server executable,
choose option 11 to install that executable, then option 9 to restart. Running
the old installed `itom` and choosing 11 reinstalls the old executable. Fresh
events should identify `itom-1-1-0-rc1-8`. Preserve profiles, app data and journals.

Confirm a measured RTT and try a small HTTPS website before SINGLE and MULTI
speedtests. If startup fails, export both full diagnostics while the failure is
present. The startup DATA check remains enabled; selected lines alone cannot
report connected.

Sustained 100 Mbps and maximum 300 ms light-load RTT remain unqualified. No
Android device or live Rambler access is available in this environment. DNS is
unchanged: race 31.131.251.83 and 185.22.234.229 over UDP/TCP 53, shared 250 ms
direct timeout, optional IMAP fallback latched until the user restarts the VPN.
