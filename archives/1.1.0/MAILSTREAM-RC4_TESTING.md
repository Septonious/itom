# ITOM 1.1.0 MAILSTREAM-RC4 — cold startup and capacity reset

Matched experimental Android/server pair. Update both endpoints. Install the APK in place, preserving app data and the existing profile. The signing identity is unchanged.

## Field failure and fix

The RC3 bundles show the 16 KiB startup probe reaching the server and being acknowledged, but the echo never returning. Approximately 413 MB remained charged to local retention. The first mutually verified account had control capacity but no bulk capacity. Other accounts had space, but their availability could only be learned from application traffic. Startup was waiting for that same traffic: a routing deadlock.

A new regression recreates this exact combination with a near-full first account, a free account joining later, and the app’s initial 16 KiB echo before any periodic ping. RC3 times out in this fixture. RC4 exchanges small authenticated per-account readiness announcements independently of application data and bulk credit. The probe and subsequent verified HTTPS complete through the available account. The announcements use the existing control sockets, stop retransmitting after confirmation, and cannot consume a new session’s reserved READY_ACK nonce.

RC4 uses fresh, versioned control mailboxes so inaccessible old control folders are no longer rendezvous dependencies. Old folders are not blindly deleted. Hard command timers close the underlying socket because the IMAP decoder can replace an ordinary socket read deadline.

## Requested local capacity policy

A manual server stop, restart, update, or IMAP purge resets the server’s local capacity counters completely. Configuration, credentials and profiles are preserved. Reset happens only after service shutdown; a live bbolt ledger cannot be changed. Purge attempts reset counters even if the provider refuses some deletions. The reported provider purge result remains separate from the local reset.

The first RC4 server start applies this policy once, including an update initiated by an older manager. Later unexpected process crashes preserve capacity state. A normal SIGTERM/SIGINT stop resets it after transport workers have joined. Manager restart and installation also perform the offline reset, including when systemd had to force termination. This resets ITOM accounting; it does not itself delete provider mail.

The controller and bandwidth settings remain unchanged. DNS settings, per-account telemetry and manager option 18 are preserved.

## Verification

Published evidence records the reproduced failure, corrected startup echo/HTTPS, command-bound tests, capacity reset and crash-policy tests, normal/race tests, native startup/restarts, weak-network cancellation/recovery, and Android compilation/tests/lint. These are local provider models; real-world verification is still required. Performance targets remain unqualified across all workloads.

## Installation

Upload the matching ITOM .bin to /tmp and run `sudo itom update-from-tmp` (manager option 18), then install the matched APK over the existing app. Keep the existing profile and app data. The first RC4 server start clears the old local capacity counters automatically. No additional mailbox deletion is required to perform that reset.
