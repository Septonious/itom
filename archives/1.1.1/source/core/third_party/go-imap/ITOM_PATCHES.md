# Local IMAP client changes

The base remains github.com/emersion/go-imap/v2 v2.0.0-beta.8. Existing UID
completion and unsolicited-response matching fixes remain pinned in the source
manifest. RC9 adds explicit cancellation of bounded streaming response delivery
for EXPUNGE, FETCH and LIST when the client closes or the decoder fails. Closing
a socket alone cannot interrupt a decoder blocked on a full response channel.
The regression fixture sends 300 EXPUNGE replies without consuming them, then
requires client close to join promptly. All modified files and this note are
hashed in ../GO_IMAP_SHA256.json.
