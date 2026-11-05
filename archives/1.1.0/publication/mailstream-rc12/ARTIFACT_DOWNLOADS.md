# RC12 startup-recovery testing files

The signed APK and matching servers are available from the successful
[verified artifact export](https://github.com/Septonious/itom/actions/runs/42057465841).
GitHub denied release creation with HTTP403, including after the verified source
tag was created. No RC12 release page is claimed. These are Actions artifact
ZIPs; sign into GitHub to download and extract them.

* [Signed Android APK](https://github.com/Septonious/itom/actions/runs/42057465841/artifacts/13428884443)
* [Linux x86-64 server](https://github.com/Septonious/itom/actions/runs/42057465841/artifacts/13429988139)
* [Linux ARM64 server](https://github.com/Septonious/itom/actions/runs/42057465841/artifacts/13429998033)
* [Sources, SHA256 checksums and evidence](https://github.com/Septonious/itom/actions/runs/42057465841/artifacts/13429863234)

All14 files were independently downloaded and SHA256 checked against the local
package. Source is45052ad0054580c9fb89f5609d7a745e2e38f447, app version10137,
org.itom.client, with the existing Android signer. Install in place and update
both endpoints. Upload the extracted server .bin to/tmp and use manager option18.
No manual mailbox cleaning is requested.

Corporate50/35 Mbps replay reaches30/30 roles and completes two cycles at
23.6/34.7 and24.7/35.4 Mbps upload/download; idle/post<=95.5 ms, loaded<=1360.9 ms.
Six actual-server startup/reconnect cycles and functional/race/Android gates pass.
The100 Mbps delayed-FETCH upload gate remains failed after two attempts;
all results are retained. Provider cleanup timeouts remain unresolved. RC10
retains its first field-ready designation, title and all14 original assets.

Actions artifact retention is90 days. The source tag and verification record
remain in Git. Performance and real-provider operation require field confirmation.
