# RC12 source checkpoint transfer
Base: 62c76953da047b836a8f9fdaa07250dcee178bb0
Exact patch SHA-256: 5ac371ce2f918753a26a9a33f31f9c23faabd86c117cd930e932d19b852b4620
Expected source tree: 6f26c4cb2f6187542278797237f49beea063d5b9
The Actions workflow applies the verified binary-inclusive patch once, commits the exact source tree to this dedicated RC12 branch, then builds and tests that source commit. No stable branch is changed. No mobile performance acceptance gate is added.
The poor-network Speedtest (2.47/1.76 Mbps, 71 ms idle, 1081/2061 ms loaded) was a VLESS reference, not an ITOM measurement.
