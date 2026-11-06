# ITOM 1.1.0 MAILSTREAM-RC16 — mobile delivery testing candidate

RC10 remains the first field-ready baseline. RC16 is not field-qualified.
The sustained real-provider 100 Mbps goal remains unverified.

RC15 mobile traces expose a flight-limited plateau: a 512 KiB allowance with
roughly 0.8–1 second bulk receipts limits download to 4–5 Mbps. All accounts
remain ready; cleanup and physical capacity are not the cause of this capture.
Small setup responses establish a faster baseline than full DATA/FETCH traffic.

The candidate permits bounded measured-rate probing when the flight allowance
blocks queued bulk work. Slow-RTT growth is limited to acknowledged bytes and a
1.75-second measured-rate horizon; independent send/delivery intervals still
protect against compressed receipts. The chosen horizon is exported in runtime
snapshots. Initial flight remains 512 KiB. No extra IMAP connections are opened.

Both a fast path with slow bulk callbacks and a true 7 Mbps link are qualified,
with repeated short full TLS/IMAP transfers and weak-link cancellation recovery.
Independent controller simulations are reported separately from full-path tests.
A VLESS comparison does not establish mail-provider route capacity. No real
Rambler or physical Android performance validation is claimed.

RC15 TLS burst classification, priority feedback, isolated request hedging and
the two-writer cleanup bound remain. Physical retention, shared memory, profile,
DNS policy, theme, application ID and persistent signer remain unchanged.
No manual mailbox cleanup or profile reset is required. Update both endpoints;
extract the server .bin into /tmp and use manager option 18. Version code: 10141.

Qualification results and verified artifact links are added after matched builds.
