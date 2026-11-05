# RC13 throughput and reclamation testing files

The matched signed APK and servers are available from the successful
[verified export](https://github.com/Septonious/itom/actions/runs/42102131372).
Sign into GitHub to download these Actions artifact ZIPs and extract them.

* [Signed Android APK](https://github.com/Septonious/itom/actions/runs/42102131372/artifacts/13450591477)
* [Linux x86-64 server](https://github.com/Septonious/itom/actions/runs/42102131372/artifacts/13450327133)
* [Linux ARM64 server](https://github.com/Septonious/itom/actions/runs/42102131372/artifacts/13450227332)
* [Sources, checksums and evidence](https://github.com/Septonious/itom/actions/runs/42102131372/artifacts/13450107546)

All 14 exported files were independently downloaded and matched against their
expected SHA256 digests. Source tag `v1.1.0-mailstream-rc13` points to
`d59dbc47c10673d753daadf8e4c7bfb51f97899d`. Android version code is 10138,
app ID remains `org.itom.client`, and the persistent signer is unchanged.
Signed APK SHA256:
`ea08fc91ba40968fc3eaddd1edc00cd2250748c5b0c723e8f956dd8bb3a06a90`.

Install the APK in place and update both endpoints. Upload the extracted server
`.bin` to `/tmp` and use manager option 18. No manual account cleanup or profile
reset is required. Additional diagnostics expose APPEND command cost, DATA/control
packing, queued bytes and account-specific collection failures.

The frozen build and independent application acceptance passed. The new service
cost model averaged 104.01/103.50 Mbps upload and 128.84/126.02 Mbps download across
two full 30-second cycles; maximum idle/post RTT was 223.62 ms and loaded RTT
580.16 ms. All modeled transfers completed with 30/30 roles and zero account
failures. Weak-link recovery, control history, account interference, storage reuse,
six actual-server startup/reconnect cycles and shutdown capacity reset passed.

These averages do not establish 100 Mbps in every five-second window. All strict
misses and the original local race package timeout are preserved in the evidence;
the separate local mailstream race run and complete unchanged hosted race gate
passed. Live Rambler and Android-device performance still require field testing.
RC10 remains the first field-ready candidate, with its original title and all
14 asset identities checked unchanged after this export.

GitHub denied RC12 release creation with HTTP403. RC13 therefore uses verified
Actions downloads; no RC13 release page is claimed. Artifact retention is 90 days.
The source tag, signature restoration data and verification records remain in Git.
