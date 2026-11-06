# RC14 repeated-transfer testing files

The matched APK and servers are available from the successful
[verified export](https://github.com/Septonious/itom/actions/runs/42183371588).
Sign into GitHub, download the ZIPs and extract them.

* [Signed Android APK](https://github.com/Septonious/itom/actions/runs/42183371588/artifacts/13486398111)
* [Linux x86-64 server](https://github.com/Septonious/itom/actions/runs/42183371588/artifacts/13486293207)
* [Linux ARM64 server](https://github.com/Septonious/itom/actions/runs/42183371588/artifacts/13485884131)
* [Sources, checksums and evidence](https://github.com/Septonious/itom/actions/runs/42183371588/artifacts/13486398127)

All 14 files were independently downloaded and matched against their SHA256
digests. Source tag `v1.1.0-mailstream-rc14` points to
`d66775cfbd59b7c0c9b225af628032723dbe9147`. Version code is 10139;
app ID `org.itom.client` and the persistent signer are unchanged. Signed APK
SHA256: `e623f7ab68429eb77fbec9b240325065dd49946d8c030edf6aeef2e524d1e891`.

Install the APK in place and update both endpoints. Upload the extracted server
`.bin` to `/tmp` and use manager option 18. No manual mailbox cleanup or profile
reset is required. Collection now uses bounded pages and tagged completion
fences; historical recovery finishes a frozen scope. Journal growth releases
receipt locks, and audited refunds reuse durable high water without disk writes.
New diagnostics separate STORE, EXPUNGE and audit stage times.

The hosted three-cycle replay averaged 117.0/118.6/116.8 Mbps upload and
144.3/129.1/118.4 Mbps download. Maximum idle/post RTT was 230.8 ms and loaded
RTT 603.3 ms, with all 30 lines, no account failures and no cleanup quarantines.
An independent local replay averaged 116–117 Mbps upload and 130–148 Mbps
download. Frozen reference failures, complete normal/race suites, Android tests
and lint, weak-link cancellation recovery, and six actual-server startup and
reconnect rounds passed. The 15-account rounds brought up all 45 lines.

A separate replay started with 534,773,760 bytes of persisted debt. All cold
transfers completed, and production audits cleared the debt to zero. Cold uploads
were 29.7/35.6/68.5 Mbps; after recovery the final pair reached 119.7 Mbps upload
and 125.7 Mbps download. Cold recovery does not meet the 100 Mbps bandwidth goal.
Other strict ramp/window/stall misses and all earlier failed prototypes remain
in the evidence. Full-phase averages do not establish every-window sustained
performance. Live Rambler and Android-device performance need field testing.

RC10 remains the first field-ready candidate; its title and all 14 asset identities
were checked unchanged. GitHub previously denied release creation with HTTP403,
so RC14 uses verified Actions downloads. No RC14 release page is claimed.
Artifact retention is 90 days; source, signature restoration data and verification
records remain in Git.
