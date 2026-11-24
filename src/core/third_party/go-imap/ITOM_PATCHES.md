# Local IMAP client changes

The base remains github.com/emersion/go-imap/v2 v2.0.0-beta.8. Existing UID
completion and unsolicited-response matching fixes remain pinned in the source
manifest. RC9 adds explicit cancellation of bounded streaming response delivery
for EXPUNGE, FETCH and LIST when the client closes or the decoder fails. Closing
a socket alone cannot interrupt a decoder blocked on a full response channel.
The regression fixture sends 300 EXPUNGE replies without consuming them, then
requires client close to join promptly. All modified files and this note are
hashed in ../GO_IMAP_SHA256.json.

ITOM 1.3.0 RC3 accepts streamed BODY-before-UID responses when exactly one
matching UID FETCH is pending. Multiple possible commands remain unassigned;
the carrier still validates the eventual requested UID and body limits. This
adds no unbounded literal buffering. The update/shape regression includes UID
following the BODY literal and RFC822.SIZE.
