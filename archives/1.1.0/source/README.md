# ITOM 1.1.0 MAILSTREAM-RC11 — experimental upload testing candidate

RC10 remains the first user-confirmed field-ready baseline. RC11 changes client
upload flight headroom and delivery-rate sampling, and increases the server
upload FETCH batch from eight to 32 already-available containers. Initial credit,
server download control, mailbox ownership/cleanup, DNS, app identity and signer
are preserved. New body receipt RTT diagnostics support field comparison. Update
both endpoints for the upload changes; preserve app data and profiles.

Update both matched endpoints. Install the signed APK in place, preserving app
data and profile. Upload the architecture-matched ITOM `.bin` to `/tmp`, then
use manager option 18 or `sudo itom update-from-tmp`. RC7 uses a new control
namespace and must not be mixed with RC6.

RC10 retains RC7's measured-rate controller. It removes idle control-report
feedback, defers historical sweeps while the VPN has ample capacity, and
amortizes DATA collection over 128 containers. Capacity pressure and clear
notifications work in both directions without authorizing refunds. Current DATA
collection cannot be diverted by unrelated historical debt. Install both RC10
endpoints; their pressure notifications require matching transport code.

RC7 collects consumed control prefixes by
UID, so ordinary EXPUNGE cannot shift the reader's cursor. Successful tagged
APPENDUID ownership and a verified empty UID prefix are required for refunds.
A fenced namespace inventory collects abandoned partial DATA and old control
folders. Idle control tails are collected only while all server sessions
are idle; fresh admission interrupts that maintenance. Empty folders are kept
because provider DELETE can lock the whole account. Unrelated mail and live
session DATA are excluded. The server must remain running for idle maintenance;
if stopped immediately, leftovers are revisited after the next start.

Failed collection quarantines that account/folder, preserves its charge and
retries with bounded exponential backoff. Collection cannot close delivery readers. Optional control-tail cleanup yields
during bulk DATA when storage has headroom, then resumes at a quiet interval. The normal 64 MiB storage
budget and 64 DATA slots remain; an additional finite 64 KiB durable reserve
carries only bootstrap/recovery messages when an old ledger is full. Application
traffic cannot use that reserve. Provider storage is never freed by resetting
local counters alone. Manual stop/restart/update/purge retains its requested
server-local reset behavior.

RC6 cancellation, half-close, native DNS/TCP admission, per-account diagnostics,
stream windows and congestion controller are retained. DNS races 31.131.251.83
and 185.22.234.229 over UDP/TCP 53 for 250 ms. Optional IMAP fallback starts only
after both fail and lasts until VPN restart. No DoH/DoT or theme changes.

See [the transport contract](mailstream/REWRITE.md). Source/normal/race/native/
SDK/vet gates use `python3 tools/build.py core --race`. Independent TLS IMAP/HTTPS
qualification measures actual verified bytes, not controller estimates. Targets
remain 300 ms idle/light RTT, 2000 ms loaded RTT and sustained 100 Mbps each
direction where path capacity permits. Real Rambler and Android/mobile behavior
still requires field confirmation. Permanent provider mutation failures can
exhaust finite storage; empty folders are retained deliberately; automatic cleanup never uses DELETE.
