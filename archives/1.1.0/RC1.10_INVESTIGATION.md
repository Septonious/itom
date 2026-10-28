# RC1.10 — retained-mail backlog and historical recovery contention

The paired RC1.9 exports show all twenty lines selected, Noise and roster
complete, then PATH timeout. Both endpoints identify RC1.9. The client recovery
queue reaches 64 records before PATH. The first OPEN reaches the server after
the client's original 1.5-second OPEN deadline. The client has not sent probe
DATA yet. The later server EOF retains EXPUNGE alone; the RC1.9 APPEND/EXPUNGE
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
* Historical grant/fence/status/retirement work has one shared queued/in-flight
  logical slot. Current permission messages and live stream work have priority.
  Unknown physical publication keeps its slot and original debt; it does not
  block independent live work. Recovery continues when live work yields.
* Diagnostics expose historical read hints and background-authority outstanding
  counts, without UIDs, session keys, destinations or traffic contents.

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
V2_STARTUP_PATH in 32.42 seconds. The combined correction completes both sessions
and HTTPS in 47.59 seconds. A first attempt with only read handoff still failed
PATH; isolating historical authority traffic was also necessary. These are
whole-cohort times, not throughput or raw RTT measurements.

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
