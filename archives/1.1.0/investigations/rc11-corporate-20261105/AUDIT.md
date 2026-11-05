# RC11 corporate-network account startup audit

Both uploaded endpoints identify themselves as `itom-1-1-0-mailstream-rc11`.
The directly measured access link is 50 Mbps down / 35 Mbps up. The reported
VPN speedtest is 5.59/0.77 Mbps, idle RTT 384 ms, upload-loaded RTT 3890 ms.
RC10 remains the first field-ready baseline and is not replaced or relabeled.

## What the captures establish

Both endpoints lose exactly accounts a02, a03, a05 and a10. All four stop at
`control_select` with `MAIL_CARRIER_CLOSED`, zero control cursor and no peer
verification. Six accounts remain ready, accounting for 18/30 active roles.
The same server failures occur across service restarts. This is not evidence
that only the client Wi-Fi link is at fault. LOGIN account checks do not perform
the same mailbox setup as the carrier.

In frozen RC11, `selectLog` unconditionally issues CREATE before SELECT, even
when the control folder already exists. A non-tagged CREATE error maps to
ErrClosed and this exact failure label; SELECT errors instead map to
ErrProtocol. The encompassing setup timer closes the transport after 15 seconds.
The captures lack transaction-level command responses, so they cannot distinguish
a provider-stalled CREATE from a transport failure while executing CREATE.
A provider lock or damaged folder is a hypothesis, not a confirmed root cause.

Any setup failure returns from the account owner, whose deferred `fail()`
cancels that account permanently for the lifetime of the link. Healthy accounts
keep working, but the failed accounts never retry. Cleaning mail cannot fix
that lifecycle defect within the session.

At the last client snapshot, flight is only 7357 bytes with the original
512 KiB allowance and zero measured bulk rate. At earlier failed-upload samples
flight remains near that initial allowance. There is no evidence that the new
large measured-rate allowance alone explains this failure. The captures do
show 243 client retransmissions and large per-account RTT differences despite
some responsive NOOP samples. All account retention totals remain below the
finite 64 MiB per-endpoint limit. Server cleanup remains paused at
maintenance_expunge; unknown outcomes must stay charged.

## Focused correction

* SELECT existing inbound control folders before considering CREATE; STATUS the
  outgoing folder before considering CREATE. Explicit temporary/permission
  response codes do not request CREATE. Fresh folders still initialize, and
  simultaneous peer creation remains supported.
* Retry temporary setup failures on fresh sockets, releasing the four-slot
  login gate between attempts with bounded exponential backoff and account
  staggering. Credentials/storage failures remain terminal. Account context,
  authenticated peer discovery and healthy established carriers are preserved.
* Expose startup attempt count and next retry time in diagnostic snapshots.
  A successful account clears its old failure and joins the existing session.
* Retain the RC11 delivery controller and RC10 download behavior. No additional
  flight growth, provider folder DELETE or speculative capacity refund is added.

These changes address the demonstrated startup defect. They do not establish
that every corporate-network performance problem or the provider's cleanup
stalls have been eliminated.

## Reproduction and acceptance

Independent verified-TLS IMAP fixtures exercise an existing-folder CREATE that
blocks while SELECT/STATUS and LOGIN work, and a temporary SELECT refusal on
one account while HTTPS already works through another. Both scenarios fail on
frozen RC11 source; both pass on the corrected implementation. The latter
requires the account to join the same live session and checks that the healthy
account has exactly one setup attempt.

The provider shaper now supports independent shared uplink/downlink budgets,
in addition to its existing shared-radio budget. A four-account startup fault
model uses 35 Mbps up / 50 Mbps down and the same affected account IDs. Frozen
RC11 must reproduce exactly 18/30 roles. The candidate must reach 30/30 despite
temporary SELECT refusals, avoid all blocked redundant CREATEs, complete two
upload/download cycles and remain inside 300 ms idle / 2000 ms loaded bounds.
This is a controlled hypothesis replay, not an exact emulation of Rambler.

Local prototype results: four-stream cycles reached 24.58/34.61 and 24.49/35.36
Mbps upload/download with all 30 roles. Lower payload goodput than the direct
35/50 Mbps link is expected because MIME/base64, TLS and control consume wire
capacity. The 100 Mbps payload goal cannot be required on a 35 Mbps uplink.
Exact-source hosted results, native startup checks and artifact identities are
recorded with the release; prototype observations are not substituted for them.

The previous cleanup/decoder, idle feedback, slow receipt, 7 Mbps cancellation,
account metadata interference and fast-link throughput gates remain required.
No physical storage is refunded for an unverified deletion. The release is a
matched test candidate, pending real-provider confirmation.
