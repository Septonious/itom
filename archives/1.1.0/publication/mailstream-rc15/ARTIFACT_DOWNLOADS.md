# RC15 short-transfer and UI testing files

Download and extract the ZIPs from the successful
[verified export](https://github.com/Septonious/itom/actions/runs/42198509618).
GitHub sign-in is required for these private-repository Actions artifacts.

* [Signed Android APK](https://github.com/Septonious/itom/actions/runs/42198509618/artifacts/13493447001)
* [Linux x86-64 server](https://github.com/Septonious/itom/actions/runs/42198509618/artifacts/13492773408)
* [Linux ARM64 server](https://github.com/Septonious/itom/actions/runs/42198509618/artifacts/13493312288)
* [Sources, checksums and evidence](https://github.com/Septonious/itom/actions/runs/42198509618/artifacts/13492623894)

All 14 exported files were independently downloaded and SHA256 matched.
Source tag `v1.1.0-mailstream-rc15` points to
`3791869c5e15250b8d4070c7bc8a03794c0c8c89`. Android version code is 10140;
application ID `org.itom.client` and the persistent signer are unchanged.
APK SHA256: `4a470521f4ce1479e6f8a8083f381e199cec731f6a2b25fc45edeb19e244edf6`.

Update both endpoints. Install the APK in place; upload the extracted server
`.bin` into `/tmp` and use manager option 18. No manual mailbox cleanup or profile
reset is required. Paired administration cards now share their row's intrinsic
height while preserving the existing theme and wrapping descriptions.

TLS-sized byte bursts become bulk instead of mirroring every small record.
Isolated requests retain immediate treatment and hedging. Priority feedback is
batched briefly, queued control is respected across MIME boundaries, delivery
growth uses authenticated byte feedback in both directions, and cleanup can
borrow at most two writers including preparation. The initial 512 KiB flight,
physical byte budgets, MIME/bundle bounds and connection counts are unchanged.

The final hosted 16-stream, 10-second MULTI uploads were 114.3/106.5/106.7 Mbps;
downloads were 178.7/183.2/167.0 Mbps. Frozen RC14 uploads on the same scenario
were 89.2/84.3/87.9 Mbps. Upload ramp to a rolling one-second 100 Mbps improved
from 4.5/5.5/4.5 seconds to 2.5/3.5/3.5 seconds. Every candidate rolling
two-second interval starting after second two exceeded 100 Mbps. Maximum
idle/post RTT was 235.2 ms and loaded RTT 503.1 ms; borrowed writers peaked at
two, compared with six for RC14. SINGLE uploads were 118.2/118.9/116.3 Mbps.

All eleven matched build/acceptance jobs and independent source acceptance
passed, including normal/race suites, weak-link cancellation recovery, retained
storage and historical regression checks. The actual server binary passed six
startup/reconnect rounds with 10 and 15 accounts and manual-stop capacity reset.
The focused local race cohort passed 24 scenarios. Android compiled both JNI
architectures, passed 101 unit tests, and had zero fatal lint errors.

The two-second ramp target remains unmet. Cold old-debt recovery can be slower
until audited capacity returns. All strict misses and failed prototypes remain
in the evidence. These are synthetic results; live Rambler, Android-device speed
and physical layout still need field confirmation. RC10 remains the first
field-ready candidate, with its title and all 14 asset identities unchanged.

GitHub previously denied release creation with HTTP403, so these are verified
Actions downloads; no RC15 release page is claimed. Artifacts retain for 90 days.
Source, public signature restoration data and verification records remain in Git.
