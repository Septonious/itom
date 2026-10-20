# ITOM 1.0.0 downloads

[Release and all files](https://github.com/Septonious/itom/releases/tag/v1.0.0) · [Build gates](https://github.com/Septonious/itom/actions/runs/39430001782) · [Successful publication and remote digest verification](https://github.com/Septonious/itom/actions/runs/39433803819)

| File | Size (bytes) | SHA-256 |
| --- | ---: | --- |
| [ITOM-1.0.0-debug.apk](https://github.com/Septonious/itom/releases/download/v1.0.0/ITOM-1.0.0-debug.apk) | 72128018 | `7eef05aec9172f2d8d85005fd87c866d1a6cbefbf99e0a020c6f294e67884402` |
| [ITOM-1.0.0-full.zip](https://github.com/Septonious/itom/releases/download/v1.0.0/ITOM-1.0.0-full.zip) | 47409111 | `d6db847bb11186b4203b76ff5dfc34bc450f404a60570dcfad6d0d1e6f72cf78` |
| [ITOM-1.0.0-linux-amd64.bin](https://github.com/Septonious/itom/releases/download/v1.0.0/ITOM-1.0.0-linux-amd64.bin) | 14468586 | `c2cad47c8fc64a7ff78cb09704517be4b99d469dd56881d52973efe9f438cb8f` |
| [ITOM-1.0.0-linux-arm64.bin](https://github.com/Septonious/itom/releases/download/v1.0.0/ITOM-1.0.0-linux-arm64.bin) | 13299802 | `be72b590563168a672c23450dd2a34d603fe7f5b867dc382f14484cf40389663` |
| [ITOM-1.0.0-sources.zip](https://github.com/Septonious/itom/releases/download/v1.0.0/ITOM-1.0.0-sources.zip) | 2475021 | `5bc58c770848d0f3e58d6fcb580d787ba246614c108d10ded07c386017d23497` |
| [lint-summary.json](https://github.com/Septonious/itom/releases/download/v1.0.0/lint-summary.json) | 568 | `7090c7e800e67cd4e185e0dc4135a60aa1dd2eab69e63d8579e7d719ed1d243f` |
| [POST_RC2_CHANGES.zip](https://github.com/Septonious/itom/releases/download/v1.0.0/POST_RC2_CHANGES.zip) | 1416478 | `2911451b357457bf96743945a8956541cf48cc0736fcb7db9ef9c03e9b3d4db7` |
| [RELEASE_NOTES.md](https://github.com/Septonious/itom/releases/download/v1.0.0/RELEASE_NOTES.md) | 2377 | `894863c163dc90851b1ec081114b29ace2947b138849e7710deb0f4fc925a482` |
| [SHA256SUMS](https://github.com/Septonious/itom/releases/download/v1.0.0/SHA256SUMS) | 693 | `97007df70c4fc3f446bc40d0b9865682ca2cba39c9bbafc525f12d35974f7fe7` |
| [SIGNER.txt](https://github.com/Septonious/itom/releases/download/v1.0.0/SIGNER.txt) | 307 | `5fdea5c7e9f41524949a48a71fdae5ac079e3118447f218f77d779a32d8163d3` |
| [test-summary.json](https://github.com/Septonious/itom/releases/download/v1.0.0/test-summary.json) | 165 | `df76bd1e8f6eb1e38ff897f5a24feb6e24c60a41396be9772aa9d1fb79218454` |
| [TESTING_NOTES.txt](https://github.com/Septonious/itom/releases/download/v1.0.0/TESTING_NOTES.txt) | 569 | `c039ab410379adb2269a47e6417744a5f22d2ce6a0fa61670a252fa7ab1635e5` |

Transport is RC2; only release labels and Android packaging identity/signing guards are updated. APK application ID org.itom.client, versionCode10014. Signing uses a temporary debug certificate; update compatibility with older APKs is not assured. Preserve app/profile/wallet and server journal state. Never clear data to bypass signing errors.

Core correctness/vet/race and Android SDK tests/lint/native identity passed. This is the user's RC2 final-release choice; no new sustained100Mbps/sub200ms or complete field qualification is claimed. New timing prototypes remain unaccepted; the failed local RC2 controls and calibration requirement are preserved in [the experiment report](../../docs/1.1.0/experiments/RESULTS.md).
