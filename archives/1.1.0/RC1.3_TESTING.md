# ITOM 1.1.0 RC1.3 — retained-mail startup fix

Experimental matched client/server release. Update both endpoints; the stable 1.0.0 release is unchanged. Sustained 100 Mbps and the 300 ms idle/light-load ceiling are not newly qualified by this startup release.

## Investigation of the October 26 RC1.2 exports

The current server trace identifies four service starts with 15 accounts and two users. Each fails during retained-mail inventory before rendezvous or a client handshake. The first causal failure repeatedly comes from a retained folder on one account. The service then cancels sibling logins and the other user; the resulting CONNECT/TLS/GREETING errors are not evidence that every account independently failed TLS. Successful SELECT replies advertise UIDPLUS and permanent keywords.

The client archive contains an OFF snapshot, no transport state and no trace files. It cannot establish the phone's failure stage. Rotated server files contain the older transport's historical events; those are not RC1.2 runtime measurements.

The installed ledgers also show substantial historical DATA reservations for one user: about 81–95% of the original byte allowance and 164–181 of 192 message slots per account. A quarter of the full configured limit cannot be issued from that remaining space. Neither reinstalling the phone nor moving mail is permission to refund those reservations.

## Changes

- The session transport uses `<profile-up>-session-v2` and `<profile-down>-session-v2`. Earlier control formats and DATA shards stay in their original folders; they are not fed to a v2 decoder. This fixes the protocol namespace collision reproduced from the installed upgrade path.
- Each original journal gains only the two allowlisted folders, in one audited, capacity-neutral transaction. Existing issuer identities, capacities, all legacy folders, uncertain APPENDs and outstanding reservations remain. No second ledger, blanket cleanup, wallet reset or timer refund is introduced. The new namespace receives its own complete bounded inventory before admission.
- Both endpoints may idempotently CREATE their exact planned v2 folders and then verify SELECT. This handles the phone racing the first server start without arbitrary folder creation or an unaccounted APPEND.
- New remote grants use a bounded share of headroom after historical local and remote debt. Clean stores retain the previous grant sizes. Original journal admission is still authoritative. Historical state can continue to restrict throughput; this release does not claim to reclaim old mail or recover missing wallet history.
- Account startup preserves its first causal error and stops starting queued logins after cancellation. Cancelled bootstrap operations report cancellation instead of misleading secondary TLS failures.
- App diagnostics retain an allowlisted last-startup report across Stop and process restart, even when full tracing is off. Export uses this retained report when the live session has gone away. It contains stage, selected socket count, outcome and fixed failure codes, not credentials/provider text/payload. Selected sockets are not reported as usable VPN lines.

## Verification and limits

Before/after reproduction: populated old physical MIME folders, original journal with an unresolved APPEND, and a nearly full historical reservation. Before namespace isolation, the server fails inventory and the client times out at rendezvous. After isolation but before headroom allocation, the client stalls at permission. The complete fix carries real HTTPS and restarts the VPN with the same state paths while the old mail and unknown debt remain.

Additional coverage includes all 12 installed DATA capacity/shard combinations (8/12/16 MiB, 0/1/2/4 shards), migration rejection for changed identities/limits/unowned folders, and a 15-account/two-user public API fixture. The latter uses real verified TLS IMAP commands, changing 20/40/30 ms command service, original application deadlines, and checks 30 usable lines per phone session. These are protocol simulations, not live Rambler/Android-device or sustained-throughput measurements.

Uncalibrated zero-delay 15-account experiments with the race detector exceeded an original HTTP deadline locally; those failures and CPU profiles are retained in the release evidence. The corresponding uninstrumented zero-delay run passed. Do not use that run to claim a real-world RTT or bandwidth improvement.

The release build and attached evidence provide the final test results. No real Rambler credentials or field diagnostic payloads are included in the public evidence.

## Install and collect a useful retest

1. Run the newly downloaded server binary and choose **11 / install**. This updates `/usr/local/bin/itom` while preserving `/var/lib/itom`. Menu 7/9 start or restart the installed binary; they do not install the downloaded copy.
2. Update the APK in place. Keep the existing profile and app data. No profile re-import is needed when both endpoints use RC1.3.
3. Confirm revision `itom-1-1-0-rc1-3` on the actual running server. Use server tracing option 14 and export option 13 if startup still fails. The app's full diagnostics export now retains the last attempt after disconnecting.

Both endpoints must be updated because the physical namespace/configuration binding changed. No private account, journal or wallet files should be deleted to make this release connect.
