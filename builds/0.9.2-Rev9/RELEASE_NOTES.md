# ITOM 0.9.2 Rev9 — targeted testing build

Optional IMAP IPv4/IPv6 pinning is now available in the server manager, guarded admin UI and Android profile editor, for one account or all accounts. Exported invitations carry the pin. TCP connects to the pin while TLS continues to authenticate the IMAP hostname; blank restores DNS. Existing persistent sockets, IDLE and idle NOOP keepalive remain in use. The tests verify one authenticated connection across multiple operations.

Profile-guided changes remove redundant ITE2 proof hashing, repeated BINARY MIME decoding, repeated carrier-score scans and per-socket recounting of bulk readers. Digest, descriptor, quota, Noise/AEAD, replay and physical-deletion checks remain mandatory. Worker/queue/physical caps are unchanged.

Targeted Go race and normal recovery checks, native host checks, Android tests and lint passed. The full final core suite and physical provider/device qualification are NOT claimed; this remains a testing prerelease. The earlier race-instrumented candidate timeout remains included. The 100 Mbps floor has no speed margin.

Final retained-state laboratory results (15 accounts; 20 ms declared command delay; 600 Mbps provider model; one 60-second SINGLE and MULTI case):

- single-1: upload 100.987 / download 82.623 Mbps; adjacent RTT variation mean 88.266 ms / p95 224.254 ms; strict speed case FAIL; post-load survival True.
- multi-1: upload 79.814 / download 62.399 Mbps; adjacent RTT variation mean 121.492 ms / p95 379.585 ms; strict speed case FAIL; post-load survival True.

**Strict 100 Mbps duplex qualification is still incomplete/failed; do not treat this as a certified stable release.** The laboratory jitter target was not met in every case. No live Finnish-provider or phone performance guarantee is implied by this scenario. Read VALIDATION.json and HANDOFF.md for the exact limits.

Update both endpoints, retaining existing configuration and durable state. The APK uses version code 9209 and the existing signer/application ID. Server manager: Mail accounts → IMAP IP pinning; phone: edit profile → account pin or Set IP pin for all accounts. Server changes do not silently overwrite an existing phone profile. Private credentials/signing keys and raw user captures are excluded. GitHub Actions verifies and publishes the exact built assets, source, evidence and full handoff.
