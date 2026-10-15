# Local rework checks

Use verified Go 1.27.1, a C compiler for race tests, Python 3.10+, and ordinary verified Go module access. Do not disable TLS, module checksums or race assertions. From `itom/`:

```
python tools/rework_gate.py --go /path/to/go --suite local
python tools/rework_gate.py --go /path/to/go --suite compare130 --repeats 3
python tools/rework_gate.py --go /path/to/go --suite performance --provider-mbps 600 --repeats 3
python tools/rework_gate.py --go /path/to/go --suite http --provider-mbps 600 --http-rtt-ms 65 --repeats 3
```

Use --http-rtt-ms 250 for the separately reported high-latency stress profile. The 65 ms profile is an assumed healthy access model; neither profile is a calibrated field-provider claim.

Every invocation writes a fresh result directory under reports/rework with results.json and full logs. A failed target returns nonzero. Performance requires 50 Mbps useful UL/DL full-window and final-tail delivery, complete measurement windows and application RTT<1000 ms. The same transport must survive repeated stress, then pass verified same-connection exchanges after an idle gap. The original 100+ Mbps target remains an aspiration. Account/provider models remain synthetic. The 130 Mbps physical profile is retained as a historical comparison. Current qualification uses the separately declared 600 Mbps physical profile; useful application throughput and physical encoding/service costs are distinct.

The existing clean build launcher remains:

```
python tools/build.py all --expect-revision transport-rework1-field79
```

This command needs Android prerequisites/licenses and builds matching sources; its successful build does not replace the performance and field gates. The source archive is for GPT 6.1 continuation. Production deployment and Android/device or real-provider qualification are separate work.

The setup machine's gVisor ZIP redirect at storage.googleapis.com was denied. The exact pinned source commit 69c2d17aea96 was fetched from codeload.github.com, packed using golang.org/x/mod/zip.CreateFromDir with the pinned module identity, and Go's download verified it against the unchanged native/go.sum. No checksums or dependencies were changed. Normal network access to the module proxy and its storage destination is the preferred reproduction route; the module cache and toolchain are not bundled.
