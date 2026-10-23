# ITOM 1.1.0 DEV8 — testing RC

This package fixes two reproduced receive-path defects: live control reads no longer wait for unrelated account maintenance, while keeping the previous shared-socket DATA service share; short responses beyond freshly deleted UID windows no longer wait for the periodic allocation audit. Dense reads retain their original command budget. Quota, wallet, journal, recovery proofs and physical limits are unchanged.

The matched original lab qualification passed **20/20 sustained rounds and 48/48 short phases**, versus unchanged RC2's 16/20 and 42/48. Core correctness, vet, accounting/mailbox/recovery races, 89 Android unit tests, APK compilation and lint passed (one dependency-version lint warning). These are laboratory results, not a prediction of Rambler/device speeds. The older broad profiles have more socket/permission headroom and different provider capabilities than the captured deployment; their 200–280 Mbps measurements do not establish real-world bandwidth. Normal-profile upload means remain about 8–9% below RC2 while download increases.

The closer 15-account simulation preserves 48 historical grants per account, the captured three-server/two-phone socket roles, a 20 ms server path and 40 ms phone path. SINGLE means: **108.1/78.2 Mbps**, versus DEV7's 108.2/79.2. MULTI: **105.0/76.9 Mbps**, versus 106.0/79.9. All 12 phases complete, but only 3/12 pass both the unchanged 100 Mbps floor and zero half-second stall requirement (DEV7: 5/12). Those failures remain failures.

**Known limitation:** the 40–180 ms mobile sensitivity still has light-request/TLS timeouts. The code fixes specific reproduced faults; it is not yet field-qualified and does not establish that every reported Speedtest failure or idle spike is fixed. The full numeric results and raw logs accompany this release. Earlier rejected allocation, hot-polling and routing experiments are not in these binaries.

Install **both** DEV8 endpoints. Update the APK in place; it uses the same persistent signer as DEV5–DEV7 and the unchanged `org.itom.client` identity. **Do not clear app data, erase wallets or reset server journals.** Keep the server's existing state directory when replacing its binary.

Runtime/build source: `6ade0182848652152fd8aa9af7a8078dfda27563`. The release tag points to a publication commit whose core, native and Android sources are identical. Verify downloads with `SHA256SUMS`.
