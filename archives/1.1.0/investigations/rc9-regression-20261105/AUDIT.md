# RC9 field regression audit

The November 5 client/server exports identify RC9. All 30 endpoint roles remain
connected; no capacity refusal or failed account explains the 5–6 Mbps download,
0.8–1.0 Mbps upload and 285–295 ms idle RTT reported by the user. RC7 remains the
best field baseline: 173–198 ms idle RTT, 38.6–77.2 Mbps download, 4.17–7.59 Mbps
upload in the preceding successful samples. These are separate field runs, not
controlled comparisons on an identical network.

RC9's server trace records 243 cleanup completions during roughly three minutes;
77 of 91 sampled operations target current control folders. Historical recovery
is still incomplete at the end. The client carries 95,406,068 bytes of prior
conservative debt; manual server reset explains its asymmetric zero prior debt.
The trace does not prove the mail provider's internal locking implementation.

An implementation defect is independently reproducible: while historical
recovery is incomplete, every advancing control cursor causes a new watermark
report after one second. Reports produce packet receipts and more control UIDs,
which produce more reports. The same incomplete-history flag also prevents
bulk deferral, and historical work receives priority over DATA collection.
RC7's stuck cleanup masked this work; RC9's decoder repair made it execute.
Restoring only RC7's delivery controller therefore did not restore its field
behavior.

The candidate keeps that controller and the interruptible decoder, bounded
manager commands, ordinary DNS settings and exact audited retention accounting.
It changes scheduling: report on initial attachment and real watermark batches;
request urgent historical recovery only when historical debt actually threatens
capacity; otherwise defer namespace sweeps until no VPN is active. An
authenticated bidirectional pressure message requests recovery without refunding
any debt. This covers a reset server paired with a full client, and the inverse.
Current DATA collection follows its own exact ownership proofs and cannot be
diverted into historical recovery merely because its backlog is large.

A new stress model adds 40 retained old DATA folders and 300 old control messages
per account, 20 seconds of idle observation, independent variable endpoint RTTs,
and 40 ms account-wide metadata service costs. This is a deliberately adversarial
provider model, not a measured assertion about Rambler. RC9 fails a later upload.
The first candidate also failed a later download; excluding current DATA from
historical-pressure triggering restores completion, but the next upload reaches
only 46 Mbps. These unsuccessful prototypes are retained in private evidence.
The next candidate amortizes collection over 128 bounded MIME containers instead
of 32. This changes neither small-packet publication timing nor the 64 MiB
physical retention limit. Final qualification results are recorded separately.

No automatic folder DELETE is introduced. Empty owned folders remain. Idle
cleanup requires the server process to remain running after VPN disconnection.
No credentials, account addresses, profile keys or raw field archives are
included in this audit.
