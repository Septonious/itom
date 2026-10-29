# RC1.11 investigation and release boundary

RC1.10 was tested after publication using its downloaded server executable,
the exact packaged production sources, persistent real journals/wallets and a
local TLS IMAP provider. Clean startup, legacy unknown debt, a 128-body retained
backlog, authenticated native administration and native UDP DNS/SOCKS/TUN HTTPS
passed. Extending the run to twenty VPN restarts exposed a reproducible startup
failure: eleven restarts succeeded and the twelfth could not allocate startup.
That failure is preserved under verification/rc1-10/POSTRELEASE_BEFORE.zip.

## Causes and corrections

1. Confirmed bootstrap/roster bodies were skipped at runtime without entering
   reclamation. The private completed handoff now carries original observed
   attempt references and authenticated per-body receipt candidates. Hints
   alone still cannot authorize deletion or release physical debt.
2. A small remaining quarter-allowance could not complete startup. Startup now
   reserves exactly four CLIENT publications and requires original capacity
   for those plus six SERVER publications. No limit is increased. Light CONTROL
   leases cap new reservations at eight messages; actual bulk demand restores
   the original allowance and DATA reservations are unchanged.
3. One quota-blocked account globally overrode foreground scheduling. Local
   cleanup now responds to its own account. Global historical launches override
   the original short-request grace only when every eligible account in a
   ledger is blocked, or during bulk/quiet service.
4. Stops could interrupt reclamation after real peer receipt. Restart inventory
   may recover only our own original SERVER attempt after full MAC and original
   journal BODY matching, an exact per-body client receipt, exact UID EXPUNGE
   and independent same-incarnation absence. Missing original rows, old CLIENT
   bodies, unreceived SERVER mail and incompatible namespaces remain ineligible.
5. Final-spend fences from surviving wallets were deferred as optional history,
   keeping unused reservations held. Their exact original final spend remains
   urgent and bounded; speculative re-offers yield from session startup.
6. Wallet acknowledgement discarded the server's Closed flag until all physical
   cleanup completed. Repeated starts consequently resent already acknowledged
   fences and generated increasing status traffic, eventually delaying TLS.
   `IssuerClosed` now persists that authenticated exact-final acknowledgement
   separately from registration, release and retirement. Old JSON records default
   conservatively to false. Acknowledgement cannot make a grant spendable,
   change original Used, prove deletion or forgive physical debt.

Intermediate fixes were insufficient: later runs still exhausted capacity or
failed TLS. Their evidence is retained, including an invalid empty-journal unit
fixture corrected independently of production. Superseded full local runs were
interrupted and are not counted as successful qualification.

## Validation

The first combined correction passed all twenty persistent VPN restarts with
original DATA readiness echoes and actual HTTPS. DATA readiness samples averaged
236 ms; eighteen of twenty were within 300 ms, with maximum 341 ms. These are
cold-start samples from a local fixture, not steady idle field measurements.

Negative checks preserve unknown physical intents; require exact original BODY,
UID and incarnation; reject transplanted per-body receipts; retain debt after a
successful EXPUNGE when independent absence is missing; preserve closed spend
across wallet reopen; and prevent old registration status from reopening a grant.
Final package-specific functional, Android and artifact checks are recorded in
the release's RC1_BUILD.json and accompanying raw synthetic evidence.

No original 1500 ms OPEN/TLS/header, 30 s IMAP or 90 s startup deadline was raised.
The twenty-session fixture uses the original combined CONTROL128+DATA192
physical message capacity of 320; the durable per-ledger quotas remain unchanged.
Outer cohort time covers multiple independent original-deadline sessions.
No CPU override, blind resend, extra connection, profile reset, quota reset,
financial reconstruction from missing wallet history or private diagnostic upload
is part of this release.

This evidence does not establish live Rambler connectivity, Android-device
operation, sustained 100 Mbps or the complete 300 ms light-load RTT goal. The
published APK/server pair remains an experimental real-world testing release.
