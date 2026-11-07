# ITOM 1.1.1 final publication

The source tag pins `6ccdb6d2a408dcddadd0ea26d1cc840c6acb350a`.
The release retains the 1.1.0 controller and hard mailbox budgets. Runtime fixes
cover owned-control-folder bootstrap, feedback fallback rotation, and 32-message
retry pages after a collection failure. Healthy collection batching is retained.

[All seventeen build gates](https://github.com/Septonious/itom/actions/runs/42362639047)
and [independent application acceptance](https://github.com/Septonious/itom/actions/runs/42362639023)
passed. Qualification covers nineteen generated TLS/provider models, both Linux
builds, the Android release build, normal/race suites, mailbox bounds and six
actual amd64 server/native startup/reconnect cycles. Android ran 94 domain and
8 app unit tests with no failures/skips and zero lint errors. The persistent
APK signer and both JNI libraries are checked before export and after download.

The five-unavailable-account model first missed the 300 ms idle/post-transfer
gate at 303.872 ms; the same source and unchanged gate passed on attempt two
at 244.167 ms. Initial source/checker attempts and their strict misses remain in
the evidence. Correcting the initial checker allowed bounded exact-name LIST
verification after a concurrent CREATE; no latency, throughput or mailbox cap
was relaxed. Final model results and provenance are in ITOM_111_BUILD.json.

Personal field captures and signing credentials are excluded. The patch has
automated qualification; real Rambler/mobile behavior still requires ordinary
field observation. Empty owned folders may remain. Unrelated or unrecognized
legacy mail is preserved. The published 1.1.0 tag and all fourteen assets remain
unchanged for comparison or rollback.

[Verified export](https://github.com/Septonious/itom/actions/runs/42364106147)
was downloaded again as four archives. All fourteen files, the APK v3 signer
and both JNI hashes matched. See ARTIFACT_EXPORT_VERIFICATION.json.
