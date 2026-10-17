# ITOM 1.0.0 RC6 — stability qualification candidate

Source: RC5 90169b78f841e22f33e5ec1c684a21dabd3bc8ab plus final RC5 Actions workflow.

RC5 Actions 39063786157: correctness/race/Android/native PASS; fresh and retained stability 5/6 each. Fresh SINGLE-1: download zero on second 20. Retained SINGLE-1: download zero on second 24, max RTT 1070 ms. Retained MULTI-2: max RTT 903 ms falsely passed Go 1100ms inner gate but violates selected release policy 750ms.

RC6 requires strict max RTT <750 ms IN EVERY stability Go scenario and at the release aggregator. A one-second zero useful-byte gap in either direction remains FAIL. The mandatory 12 runs have 10 accounts, 60 measured seconds, SINGLE/MULTI ×3, fresh and retained. 100/100Mbps speed floor waived, never integrity/recovery/latency.

Unchanged: 12MiB DATA journal, 8 bulk workers, 10 physical provider sessions per account split between endpoints, 2 protected fast lanes, authentication, old quota debt, unknown APPEND charges, physical proofs, original logical TCP sessions. 16MiB remains experiment only.

A passing workflow is laboratory-only; real Android phone, Rambler, physical network/ISP and installed APK signer compatibility must be checked independently. Never uninstall or wipe journal/mail to update.
