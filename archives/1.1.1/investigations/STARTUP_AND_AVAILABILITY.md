# Missing folders and degraded availability

The Nov 7 capture contains a 1.1.0 app (`itom-1-1-0-final`) and a server
reporting `itom-1-1-0-mailstream-rc19`. Five accounts repeatedly stop at
`control_select`; five enter service. That old stage includes missing-folder
creation, so the trace does not identify the exact stalled wire command.
The server also reports EXPUNGE maintenance timeouts. The capture therefore
cannot qualify the matching 1.1.0 final cleanup implementation.

A generated ten-account TLS fixture reproduces the five-account startup stall
when missing-name SELECT/STATUS waits instead of returning NONEXISTENT. The
unmodified 1.1.0 implementation reaches only five accounts. The patch first
lists the owned control names, creates only absent names and verifies concurrent
creation. All ten accounts connect without missing-name lookups. Existing-folder
CREATE stalls and temporarily unavailable accounts retain their separate tests.

An independent regression reproduces feedback scheduling bias: unavailable
candidates donate their turn to the first healthy account. With five healthy
accounts and eighty isolated bursts, the old implementation sends forty-eight
to the first healthy account instead of sixteen. Advancing from the account
that actually accepted the burst restores even rotation. The all-healthy path,
congestion controller, windows, polling and cleanup budgets are unchanged.

Release gates add two repeated full-transfer scenarios: five initially empty
accounts among ten, and five unavailable accounts among ten. They enforce
startup/availability, useful throughput, latency, message and physical-byte
bounds. The remaining 1.1.0 acceptance gates are retained. These are generated
provider models; personal captures and credentials are excluded from published
evidence. Actual mobile/provider behavior still requires ordinary observation.

The second capture confirms matched 1.1.0 final endpoints, 27/30 roles and
119/168 Mbps down/up. Account a02 still fails control startup. Seven working
accounts report EXPUNGE quarantine; retained data remains charged and mailbox
counts stay below the hard caps. This does not prove the provider's internal
reason for the delay. A generated per-message refusal fixture exposes a real
retry weakness: byte-only pages do not shrink tiny-message batches. After an
actual collection failure the patch also caps retries at32 messages, clearing
older deletion flags outside the next page and preserving all physical audits.
Healthy-account batching and the transport controller remain unchanged.
