# Gate19 upgrade fixtures

Generated with the exact gate19 source ZIP (SHA-256 `8d82a30cd8569a74c11e7ca64983cea0e91d9f29e258fa8dd2218f698dd5c25f`), Go 1.27.1 and the included generator test. No gate19 runtime files were changed. Config: a01/mail.example/12345678, itom-u-up/down, DATA 8 MiB/128, metadata 256/256. Dummy identifiers only; journals hold metadata/digests, no credentials or payload.

`empty.db` has no grants; `dirty.db` contains Prepared/Started/Unknown/observed attempts and a closed remote grant with unregistered debt. JSON sidecars were captured before closing the old store. The new code copies these DB bytes into private temp files, checks all records/debt and fences old writers on reopen.

These test DBs must never be used as production state.

bc2dafd5917e540c2eb459dca4bcf5b3758599ed19117bce15b183b5ff1f5296  dirty.db
0aebf2ddbb042f12d45bf053077d70450f939ad20feaf1e06d11bda5108fe418  empty.db
