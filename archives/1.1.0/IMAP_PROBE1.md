# ITOM IMAP transaction probe 1 — diagnostic only

This is a standalone server-side diagnostic. It does not install, update or
replace the VPN service or Android app. A new working transport RC is **not**
qualified by this release.

The latest RC1.13 exports show completed handshake and runtime construction,
followed by a client PERMISSION timeout. The server issues no fresh runtime
grants because startup cleanup blocks the global policy. The anchor's first
EXPUNGE receives no completion for 30 seconds, after which the endpoint stops
healthy carriers too. These are observed coupling defects. The underlying reason
for the provider's missing EXPUNGE completion is still unknown.

The preserved isolation experiment fixes first-session admission and carries 27
HTTPS exchanges over 35 seconds, including the anchor timeout. Its next VPN
restart fails because requests remain tied to that dead anchor. That experiment
is rejected for release, with its complete patch and failed results retained in
`experiments/rc1-14-anchor-isolation`. No passing test substitutes for that failure.

The probe compares the session executor with the original IMAP adapter used by
RC2/DEV9. For each, it tests single-message and three-message deletion: TLS login,
isolated mailbox creation, APPEND, complete BODY verification, deletion and
independent absence. The session executor reports STORE and UID EXPUNGE separately;
the reference adapter reports its verified deletion operation as one step. This
distinguishes basic login success from actual mailbox transaction compatibility.
No claim is made yet that singleton, compact ranges or another wire representation
will fix Rambler. Both implementations pass against an independent local IMAP
server; tests also cover refusal, report privacy and the offline guard.

## Run on the ITOM server

1. Stop the installed service using ITOM menu **8**. The tool checks that it is
   stopped, so the probe cannot consume the live endpoint's socket budget.
2. Download the executable for the server architecture, then run:

   ```sh
   chmod +x itom-imap-probe-linux-amd64
   sudo ./itom-imap-probe-linux-amd64 --account a02
   ```

   The default state directory is `/var/lib/itom`; use `--state` if yours differs.
   `--account` accepts the configured account alias or email. If omitted, the
   first configured account is tested. `a02` is the failed anchor in this capture.
3. Allow up to a few minutes. The program prints a ZIP path in `/tmp` even when
   a transaction fails. Keep that ZIP for the next diagnosis.
4. Start the installed service again using ITOM menu **7**.

Only four unpredictable `itom-wirecheck-*` probe mailboxes are used. At most eight
96-byte probe messages are appended. Complete generated BODY bytes are verified
before deletion; a failed operation leaves that small isolated mailbox for
inspection. Live transport folders, existing messages, grants, wallets and journals
are untouched. No automatic purge or generic EXPUNGE is used by the new executor.

Reports contain fixed stage/error names, durations, account aliases and bounded
IMAP counters. They exclude credentials, provider response text, full mailbox
names, payloads and original uploaded archives. A diagnostic timeout is not proof
of deletion. The existing 30-second executor command watchdog is unchanged.
