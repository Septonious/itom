# ITOM 1.1.0 RC1.6 — DATA readiness and connected-session evidence

This experimental build addresses the RC1.5 report of connected lines without
usable application traffic or an RTT measurement. It fixes confirmed readiness,
measurement and export omissions. The cause of the supplied field traffic stall
has not yet been established, and sustained 100 Mbps / 300 ms light RTT are not
qualified.

Update BOTH the Android app and the server. The authenticated virtual echo exit
used by this client is new; an older server cannot complete the DATA check.
Update the APK in place and preserve all profile/wallet/journal data.

For the server, run the newly downloaded executable, select option 11 to install
that executable, then option 9 to restart. Running the older installed `itom`
command and selecting 11 just reinstalls that older executable. Options 7 and 9
alone do not install a new download. Confirm fresh events identify
`itom-1-1-0-rc1-6`; option 12 first replays historical events.

## Changes

* READY requires a successful authenticated nonce echo through real accounted
  DATA in both directions, after original grant/permission readiness. Login,
  selected lines and control delivery alone cannot report connected. A failure
  is classified as `V2_STARTUP_PATH`, retaining the actual selected line count.
* RTT now reports the real peer DATA echo completion. A persistent bounded
  stream sends 16 bytes every five seconds, with only one probe outstanding.
  OPEN time is excluded from the echo sample. Failed or mismatched echoes are
  not RTT samples. Probes do not change congestion policy or refill quota.
* With tracing enabled, the client records runtime/flow/receipt/publication
  state every two seconds and on close. With tracing enabled, the connected diagnostic export includes
  the full native state, open attempts/failures/timeouts, DNS failures and the
  existing session DNS-fallback latch. No destinations, query contents,
  credentials or traffic payloads are added to these diagnostics.
* The test suite now drives the actual native client through both SOCKS and
  raw IPv4 TUN packets, rather than testing only the core dialer. It checks
  actual HTTPS upload/download integrity and wallet-preserving restarts with
  ten accounts and fluctuating command service. A separate negative provider
  case accepts control but hides DATA, and must never produce READY.

The nonce echo is an IMAP tunnel RTT, not a website/server TCP latency or a raw
IMAP-command duration. The virtual service creates no public network listener.
All ordinary DATA/control messages, receipts and reclamation keep the original
cryptographic validation and durable quota accounting. No historical debt is
reset, and no timeout or missing metadata is treated as cleanup proof.

DNS remains unchanged: race 31.131.251.83 and 185.22.234.229 using UDP/TCP 53,
250 ms shared direct timeout, and optional IMAP fallback latched until a user VPN
restart. No DoH or DoT is added.

## Field test

Connect, confirm the app shows a measured RTT, then try a small website before
running SINGLE and MULTI speedtests. Export full diagnostics from both endpoints
while the problem is present. The previously attached server executable matched
RC1.5, but executable bytes contain no server runtime trace.

A passing DATA echo proves one small round trip, not that every external
service, DNS path, destination or sustained transfer works. The new diagnostics
are intended to distinguish those failures without another reinstall.
