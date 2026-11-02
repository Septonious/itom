# RC4 load/reconnect audit — 2026-11-02

The matched RC4 client/server logs initially show all 30 roles, successful DATA
readiness and roughly 170–210 ms account RTT. At 19:15:39 UTC there are 34 TCP
streams. Maintenance first pauses on two accounts at 19:15:43–45, then on five
accounts by 19:15:57. Those accounts report zero completed maintenance commands,
then their control cursors stop advancing and RTT grows to seconds. Other
accounts continue polling. Retained storage is about 58 MB, below the ceiling.

The user disconnects at 19:16:19; teardown retains server DATA/writer roles. A
subsequent client handshake times out after 45 seconds while the server session
owner waits for teardown. The trace cannot distinguish an extra maintenance
LOGIN hang from the first DELETE hang. Both are removed from the live path.
No claim is made that speedtest failure automatically closed the whole session.

Manager option 6 originally selected legacy Up/Down folders, unrelated to the
active stream folders. Its blank stage/reason on raw context timeout did not
prove authentication failed. The replacement checks authentication plus NOOP
and reports typed stages, without selecting legacy folders.

A local independent TLS IMAP regression poisons an account if folder DELETE is
attempted, continuing to block other connections even after the caller times
out. RC4 fails verified HTTPS at 6,578,005 bytes after two DELETEs and then hangs
in teardown. The final replacement transfers 37,748,736 random bytes in both
directions, wraps both DATA slots, and reconnects immediately without DELETE.
The fixture also poisons UID EXPUNGE because the older real-provider transaction
capture shows a 30-second UID EXPUNGE timeout after successful STORE.

The replacement uses exact consumed UID STORE, standard CLOSE on a fully drained
transport-owned slot, STATUS proof of emptiness and a separate authenticated
reclamation receipt. APPEND tags and packet receipts must be terminal before
capacity is released. Two physical slots bound folder growth within a session.
There are no live maintenance sockets or automatic historical-folder deletions.

This is model evidence, not proof of Rambler CLOSE behavior or field throughput.
Old-session debt and append-only discovery logs remain limitations. Manual
server stop/restart/update/purge still reset server-local capacity as requested;
a local reset does not remove inaccessible provider mail.

A longer, four-stream RC5 candidate run (30 seconds per direction) found another
independent failure during upload: all accounts remained healthy, but a tiny
HTTPS application ping timed out as the bulk delivery window shrank. There were
542 queued packets, 5.6 MB pending and a delivery allowance of 876 KB. Priority
DATA still shared bulk byte admission and flight gating. A focused real-HTTPS
regression holds already accepted bulk, applies the reduced allowance and
reproduces fresh TLS timeout. The finite interactive reserve makes that case
pass normally and under race instrumentation, then recovers all 4 MiB of the
original eight uploads. A second check verifies the 64-packet bound, oversized
DATA exclusion, protocol reserve and capacity return on stream cancellation.
