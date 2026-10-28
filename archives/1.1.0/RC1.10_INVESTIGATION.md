# RC1.10 — retained-mail backlog and historical recovery contention

The paired RC1.9 exports show all twenty lines selected, Noise and roster
complete, then PATH timeout. Both endpoints identify RC1.9. The client recovery
queue reaches 64 records before PATH. The client times out waiting for OPEN acknowledgement within its original
1.5-second limit and has not sent probe DATA yet. The server's first runtime
snapshot has no flow; a later snapshot has the OPEN. These clocks are independent,
so their absolute timestamps are not used to calculate a cross-host RTT. The later server EOF retains EXPUNGE alone; the RC1.9 APPEND/EXPUNGE
barrier is working. An exact provider-side reason for that later EOF is absent.

Two omissions in prior synthetic tests matter: old reserved quota was modeled,
but a large retained same-namespace BODY backlog and surviving original wallet
entries were not combined with a cold first DATA roundtrip.

## Corrections

* Full authenticated retained reads now transfer across the server's temporary
  inventory connection and its selected service connection. The client retains
  equivalent knowledge from advertisement discovery. Reuse pins account,
  folder, UIDVALIDITY, user and authentication key, and skips only sessions
  different from the new authenticated session. New/current bodies remain
  subject to ordinary receive authentication, replay and accounting checks.
* Historical authority work has eight shared queued/in-flight logical slots,
  with bounded homogeneous authority-only batches (at most eight records and
  the existing 8 KiB physical CONTROL ceiling). Current permission messages and
  live stream work have priority. Unknown publication retains original debt.
* New historical re-offers/fences/retirements wait through the original short
  request lifetime after service startup or foreground OPEN/DATA. Current quota
  and replies to admitted work remain enabled. Quiet sessions resume recovery;
  bulk pressure permits it so capacity cannot be stranded. This is scheduling,
  not a longer request timeout or a storage release rule.
* A pending historical receive leaves room within the original two CONTROL
  delivery slots for live OPEN. FAST turns bound BODY reads and fairly visit
  recent and older waiting UIDs. Bulk keeps its existing sized read grouping.
* Diagnostics expose historical read hints, outstanding background work and
  fixed-size record-kind counters, without keys, destinations or payloads.
* Roster version 3 negotiates the new authority-only containers. Older roster
  versions are explicitly rejected before runtime; update both endpoints.
  Original mailbox envelopes, journal/wallet identities and profiles persist.

Retained read hints are not delivery, registration, storage absence, deletion or
refund proof. Original wallet/journal state, MACs, nonces, physical tickets,
exact cleanup proof, quotas and all application/OPEN/TLS/IMAP/startup deadlines
remain intact. No extra socket, reconnect, blind DATA retry or DNS change was
introduced. The existing exclusive EXPUNGE lifetime remains enabled.

## Reproduction and scope tests

The new production API case uses three accounts, 20/40/30 ms command service,
128 original locally charged retained DATA envelopes per FAST mailbox, and 32
original remote CONTROL offers per account in surviving wallets. CONTROL and
DATA retain their original 128/192 message ceilings. The provider fixture's
old arbitrary 64-message mailbox ceiling is replaced only for this case by the
original combined 128+192 FAST-folder ceiling. Old retained BODY debt must stay
charged through two VPN sessions and actual HTTPS.

The unchanged RC1.9 production source fails this corrected case at
V2_STARTUP_PATH in 32.42 seconds. An initial non-race correction passed in
47.59 seconds but failed the full race-enabled HTTPS gate locally and on CI;
that candidate was not released. Queue isolation, batching or tail preference
alone also failed; all generated failures are retained as evidence.

With historical launches yielding to foreground requests, the race-enabled
case completes both VPN sessions and actual HTTPS in 48.53 seconds. The two
startup DATA echoes measure 262 and 206 ms. Whole-cohort times include cold
inventory and are not throughput or raw RTT measurements. Full-suite and hosted
qualification must be assessed separately from this focused result.

The large-backlog case's outer test-process lifetimes cover the existing
90-second startup budget per VPN attempt. The original 1.5-second OPEN/TLS/header
and DATA-check deadlines remain unchanged. An earlier harness attempt expired
its 45-second helper process during the second session; it is retained in the
evidence and is not counted as a transport failure or passing qualification.

Component race tests cover full MAC and scope checks, changed UIDVALIDITY/key,
current-session BODY preservation, foreground/current-permission priority,
idle recovery progress and retained unknown publication admission. The existing
missing-wallet and exact-debt tests remain in the full gate.

Private field archives are excluded from public evidence. Live Android/provider
connectivity, sustained 100 Mbps and maximum 300 ms light-load RTT still require
qualification. The active per-stream receive window remains 256 KiB; parser
throughput above 200 Mbps is not end-to-end application throughput proof.
