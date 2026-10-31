# ITOM 1.1.0 mailbox stream rewrite — development only

The active Android bridge and server now use the new `mailstream` module.
The former session-v2 transport and its synthetic qualification tools are
preserved under `../retired-v2/`, outside the active source modules. Existing
profiles are accepted; neither side opens or resets old wallets or journals.

This is **MAILSTREAM-DEV1**, not a testing RC. Durable retention across process
restarts, retirement of discovery logs, sustained provider performance and
Android device qualification remain unfinished. Do not replace a deployed
service with this development snapshot.

The new engine uses verified TLS IMAP, fresh Noise sessions, independent TCP
streams, immediate dispatch for small writes and bounded bulk batching. It does
not require UIDPLUS, permanent keywords or UID EXPUNGE. Connection readiness
requires an authenticated 16 KiB DATA echo and a measured mailbox ping.

Three physical roles per account are shown in the app. At ten connections per
account and two enabled users, one user can be active while the other receives
an authenticated capacity refusal. Same-user VPN restarts reuse that admission.
Dead sessions release their DATA readers after a 30-second silence lease;
this does not alter bulk pacing or cap bandwidth based on RTT.

Read [the replacement contract and remaining work](mailstream/REWRITE.md).
The performance targets remain 300 ms maximum idle/light-load RTT, 2000 ms under
load, and sustained 100 Mbps in each direction. Local integrity checks do not
establish these results on Rambler or mobile networks.

The direct DNS settings and application theme are retained. DNS races
31.131.251.83 and 185.22.234.229 over UDP/TCP 53 within 250 ms. Optional IMAP
fallback starts only after both fail and lasts until VPN restart. No DoH or DoT.

Source checks: `python3 tools/check_sources.py`. The Go gate is
`python3 tools/build.py core --race`; it covers the new module, server and native
bridge. The application scenario also runs the actual native SOCKS and IPv4 TUN
entry points over an independent TLS IMAP provider. Packaging remains a
local development action, with no field-performance qualification implied.
