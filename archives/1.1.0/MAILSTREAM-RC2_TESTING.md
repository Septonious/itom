# ITOM 1.1.0 MAILSTREAM-RC2 — constrained-link fixes

Update both server and APK in place. Keep private state and the existing profile.
Run the downloaded server binary, choose 11 (install/update service), then 9
(restart). Install the APK over the existing app with its persistent signature.

RC1 field evidence showed 32 MiB pending queues, thousands of repeated DATA
publications, about 572 MB retained without collection, delivery timeouts and
eventual account shutdown. The high-rate local fixture missed the weak-link
queueing case.

RC2 separates application receive credit from publication capacity. A delivery
window grows from authenticated receipts and applies to small DATA as well as
bulk. Application writes receive backpressure, retries back off exponentially,
and RESET discards the abandoned transfer's retry set. Storage capacity is
reserved before queue admission, preserving control capacity and live accounts.

Per-account cards now receive actual routed receipt RTT, direct IMAP command
latency, carrier byte rates, freshness and active stream participation. An
account's RTT includes the return receipt path; it is not a copy of global RTT
or a bare socket ping. Idle sampling follows normal session probes and can take
several seconds to reach every account. No extra per-account polling is added.

After authenticated replacement, the server can collect its historical v3
outbound DATA, including partially filled segments from before a process restart.
It reads message sizes, unselects without EXPUNGE, deletes, verifies absence,
then refunds actual deleted bytes. It preserves current-session DATA, inbound
client DATA, control logs and legacy namespaces. Refusal or uncertainty retains
debt. Existing reservations are not reset.

This is still an experimental release. Historical client-produced DATA and
append-only control logs are not fully reclaimed; lost private state and a fully
exhausted control budget are not qualified recovery paths. Local loopback TLS
fixtures do not establish live Rambler/carrier or Android-device performance.

First verify an ordinary HTTPS site, then single/multi speedtests. On a weak
network also cancel a speedtest and open a new site without restarting the VPN.
Export full client and server diagnostics immediately if any step fails.

DNS policy is unchanged: race 31.131.251.83 and 185.22.234.229 over UDP/TCP 53,
250 ms shared timeout, optional session-sticky IMAP fallback only if both fail.
