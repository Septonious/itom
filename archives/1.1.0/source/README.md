# ITOM 1.1.0 MAILSTREAM-RC6 — experimental testing candidate

Update both the matched server binary and APK. Install the signed APK in place,
preserving app data and the current profile. Upload the architecture-matched
ITOM `.bin` to `/tmp`, then use manager option 18 or `sudo itom update-from-tmp`.

RC5 field traces show accounts dying during reader-owned CLOSE at their first
full DATA generation. RC6 moves storage reclamation entirely off delivery
readers: readers switch to a fresh announced slot while the server optionally
collects proved-drained slots using an existing writer. One failed collection
opens a shared circuit breaker, preserves debt and keeps the other roles alive.
There is no live CLOSE, UID EXPUNGE, folder DELETE or extra cleanup login.

The release also fixes silent half-closed origins blocking VPN replacement and
separates DNS worker admission from TCP so idle DNS flows cannot exhaust TCP's
worker budget. New periodic diagnostics distinguish these failures. Stream
windows, congestion controller, direct DNS policy, settings and theme are retained.

The unchanged retained-byte cap and 64-slot fallback bound prevent unbounded
mail growth. If ordinary provider EXPUNGE also fails, delivery can continue only
within remaining finite capacity. UIDPLUS is needed for sustained server-outbound
reclamation. Old-session debt and append-only discovery logs remain limitations.
Manual server stop/restart/update/purge attempts reset server-local counters as
requested; this does not delete provider mail. Unexpected crashes preserve debt.

See [the transport contract](mailstream/REWRITE.md) for ownership, cancellation,
limits and validation. Run `python3 tools/check_sources.py` and
`python3 tools/build.py core --race` for source/normal/race/native/SDK/vet gates.
The separate qualification harness measures verified HTTPS application bytes,
not controller estimates. Targets remain 300 ms idle/light RTT, 2000 ms loaded
RTT and sustained 100 Mbps each direction where path capacity permits. This
candidate still needs real Android/Rambler validation.

DNS races 31.131.251.83 and 185.22.234.229 over UDP/TCP 53 for 250 ms. Optional
IMAP fallback starts only after both fail and lasts until VPN restart. No DoH/DoT.
