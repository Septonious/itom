# RC1.9 startup and transport audit

The RC1.8 paired exports show successful login on all 20 selected lines, then
Noise and roster negotiation, with the phone waiting for permissions. The
server fails about 2.7 seconds after starting runtime: a03 FAST closes with
peer EOF and APPEND plus EXPUNGE outstanding. Other sockets close when the
endpoint aborts. This establishes the overlap, not the provider's internal
reason for disconnecting.

The first account's CONTROL journal later reaches its original 128-message
limit. Startup was fixed to that account and readiness required usable grants
on every account. Other accounts had capacity, but could not unblock startup.
Repeated installation cannot safely repair missing wallet history: unknown
original reservations remain charged.

## Corrections

* UID EXPUNGE now owns the wire writer for its complete tagged lifetime. It
  drains all earlier commands and fences all later commands, including APPEND.
  Canceled waiters cannot abandon it; executor Close joins the lease. Ordinary
  APPEND and FETCH remain concurrent outside destructive intervals.
* The server chooses available original CONTROL headroom. The phone discovers
  advertisements on all existing FAST lanes; only a complete authenticated
  invitation for its fresh wallet session selects the bootstrap account.
  Original per-account wallet paths and issuer identities stay fixed.
* Discovery inventories actual UIDs each sweep, fetching previously unread
  bodies. Late lower UIDs and previously missing bodies remain discoverable;
  this cache never proves absence or refunds storage.
* Permission readiness accepts usable CONTROL and DATA on different healthy
  accounts, because logical authenticated authority routing is session-wide.
  The subsequent real authenticated DATA echo remains mandatory before READY.
* Historical grants are re-offered even when their ledger is full, after fresh
  permissions. Pending grant retries retain the same request, ref and amount
  when recovery changes headroom. Metadata never grants missing-history refunds.
* Bounded permission diagnostics are available before the native client exists.
  Quota, journal and wallet failures have fixed nonsecret reason codes.

## Scope reviewed and retained

The audit covers selected-lane and wallet-path preflight, discovery ownership,
request MACs, invitation binding, Noise handoff, roster, permission queues,
command ordering, destructive cleanup, cancellation, close propagation and
pre-READY diagnostics. Full MACs, nonce/replay checks, original journals and
wallets, exact UID/UIDVALIDITY scope and independent post-EXPUNGE absence proof
remain required. No deadline, quota, connection budget, reconnect loop or blind
DATA resend was added. Endpoint abort on an actual failed carrier remains
conservative; the fix prevents unsafe command overlap rather than hiding errors.

Two suspected problems were excluded: MaxGrants already bounds the live journal
scan to 256 rows, and retained-folder inventory precedes persistent FAST/BULK
selection. Successful SELECT already clears the bootstrap socket deadline.

DNS remains the configured simultaneous UDP/TCP 53 resolver race with a shared
250 ms timeout and optional session-latched IMAP fallback. Native TUN readiness
and the first authenticated DATA echo remain unchanged.

## Evidence and limits

The new destructive-command regression fails against RC1.8 with APPEND and
EXPUNGE outstanding at peer EOF. The three grant-policy regressions also fail
against RC1.8. They pass with the corrections. A production client/server HTTPS
case starts with account one's CONTROL full and the other accounts' DATA full,
then completes across two VPN sessions without resetting that historical debt.
Focused race checks pass. Complete and hosted validation will be recorded with
the release evidence; no live Android device or Rambler connection is available
here. Field connectivity and sustained 100 Mbps / maximum 300 ms light-load RTT
are not qualified by these functional reproductions.

Private field archives are excluded from public evidence.
