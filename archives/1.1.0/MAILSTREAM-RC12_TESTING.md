# ITOM 1.1.0 MAILSTREAM-RC12 — partial-startup recovery candidate

[RC10 remains the first field-ready baseline](https://github.com/Septonious/itom/releases/tag/v1.1.0-mailstream-rc10).

Both RC11 captures fail on the same four accounts during control setup, leaving
18/30 roles. RC12 opens existing control folders without redundant CREATEs and
retries temporary pre-service account failures on fresh sockets. Recovered
accounts join the running VPN; healthy carriers are not restarted. Diagnostics
include startup attempt counts and next retry times.

RC11 upload-rate protection and RC10 download behavior are retained. No further
increase in initial flight, automatic folder deletion or unaudited capacity
reset is introduced. Provider EXPUNGE stalls observed in the captures remain an
open limitation. DNS settings and the app theme are unchanged.

Install both matching endpoints. Install the APK in place to retain profiles
and settings: app ID and signer are unchanged, version code 10137. Upload the
matching server `.bin` to `/tmp`, then use server-manager option 18, or run:

```sh
chmod +x /tmp/ITOM-1.1.0-MAILSTREAM-RC12-linux-amd64.bin
sudo /tmp/ITOM-1.1.0-MAILSTREAM-RC12-linux-amd64.bin install
```

No manual account cleaning is required. For field comparison, enable logs
before connection and check whether 30/30 roles appear, then run a cold MULTI,
SINGLE and another MULTI test and reconnect the VPN. A temporarily unavailable
account may take several seconds to join; its diagnostic retry record explains
why. Persistent provider-side failures cannot be promised away by a local test.

Controlled acceptance adds separate 50 Mbps down / 35 Mbps up shaping and
four-account startup faults, with frozen RC11 as the failing reference. Synthetic
payload throughput must be compared to those directional limits, including MIME
and control overhead. These results are not live-provider qualification or a
claim of 100 Mbps on a 35 Mbps uplink.
