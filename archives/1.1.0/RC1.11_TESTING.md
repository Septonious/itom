# ITOM 1.1.0 RC1.11 — persistent startup and foreground recovery

This testing candidate repairs failures reproduced against the actual released
RC1.10 server. Eleven persistent VPN restarts succeeded; the twelfth failed
because confirmed startup and roster mail never entered normal reclamation.
The new sealed handoff carries only authenticated, fully observed current-startup
cleanup candidates. Original per-body receipts, exact UID EXPUNGE and independent
same-incarnation absence checks still gate every release of physical debt.
Restart inventory can also finish interrupted cleanup of original SERVER mail
with a valid exact per-body client receipt. It uses the original BODY observation,
UID deletion and independent absence proof. Old CLIENT mail, missing journal
rows, unreceived mail and incompatible namespaces cannot use this path.
Unknown and unobserved historical debt remains charged. Startup reserves exactly
four CLIENT messages. Runtime CONTROL and DATA retain their original quota-derived allowances.

One account's quota pressure no longer unleashes historical traffic on healthy
accounts during a short request. That account's cleanup remains urgent; global
recovery overrides foreground scheduling when all accounts in a ledger are
blocked. Original final-spend fences from surviving wallets remain urgent during
foreground grace, with at most one largest unused reservation per account and
ledger for that short-request window; speculative historical re-offers yield from the start of
the session. The wallet now persists the authenticated final-spend acknowledgement
(`IssuerClosed`) separately from physical cleanup. This prevents resending
already acknowledged old fences on each restart. Older wallet files default
conservatively to unacknowledged; no balance or original spend is reset.
Bulk and quiet service continue to recover original capacity.

Update BOTH endpoints in place. Run the newly downloaded server executable,
choose option 11, then option 9. Using the old installed executable for option 11
reinstalls the old binary. Trace revision must be `itom-1-1-0-rc1-11`.
Install the APK over the current app. Preserve profiles, app data, server state,
wallets and journals. Do not clear data to test an update.

READY still requires the original authenticated accounted DATA echo. Check a
measured RTT and a small HTTPS site before speedtests. Export both full diagnostics
if startup or traffic fails; keep the failed session present while exporting.

Application OPEN/TLS/HTTP deadlines, IMAP and startup timeouts, connections,
physical quotas, cryptography and DNS are unchanged. DNS races UDP/TCP 53 requests
to 31.131.251.83 and 185.22.234.229 with a shared 250 ms deadline and optional
IMAP fallback latched until the user restarts the VPN. No DoH/DoT.

This is an experimental candidate. Local TLS/IMAP fixture success does not prove
live Rambler or Android device operation. Sustained 100 Mbps and maximum 300 ms
light-load RTT remain unqualified. Validation evidence accompanies the release.

Final packaged-source CI passes 314 core and 14 native race-test groups, both
vet checks, 93 domain and 8 Android app tests, with zero lint errors. The exact
packaged server passes twenty persistent restarts plus clean, legacy-debt and
retained-backlog cases. Native checks cover authenticated administration, UDP
DNS and SOCKS/TUN HTTPS with body integrity; the APK is source/signature verified
but was not executed on an Android device here.

Hosted cold-start DATA echoes pass 20/20 within 300 ms (maximum 279 ms). Separate
normal and post-idle propagation gates pass 64/64 and 3/3, respectively. The
upper-range gate still exceeds 300 ms in 7/16 samples (maximum 366.32 ms). The
release therefore does not satisfy the complete RTT target or establish 100 Mbps.
