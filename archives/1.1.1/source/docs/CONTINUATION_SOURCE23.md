# Source23: queued FETCH update versus concurrent EXPUNGE

Source22 rejected: B300/C200/5loss/seed1 had26.413/43.956Mbps,18echo/max2089ms but a06 stopped after56s with IMAP_SCOPE_FETCH_PARTIAL_RETRY_EXHAUSTED. This spontaneous outage is a release blocker; the raw FAIL is retained.

After a tagged-successful FETCH containing unresolved UID-only/FLAGS updates, the reader issues bounded UID SEARCH for precisely those UIDs on the same serialized authenticated mailbox operation. A confirmed absent UID needs no BODY; complete other rows remain available and the reader connection stays live. A present UID still must deliver full size/BODY within the unchanged3 retry bound. SEARCH failure, malformed ranges or UIDs outside the exact requested set fail closed and preserve the original cursor. This read-path observation cannot refund quota or replace physical GC proof; Present() stays conservative about unsolicited updates.

Regression fixtures cover an expunged UID's queued FLAGS alongside a surviving full BODY, SEARCH NO, out-of-set SEARCH and the original present-message/missing-BODY rejection. Common complete FETCH does not acquire an extra SEARCH round trip. Existing fast physical reservation and independent wake fixes are retained; all acceptance limits remain unchanged.
