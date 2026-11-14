ITOM 1.3.0 RC1 dynamically shares mail accounts among active users. Configured offline users reserve nothing; connected idle users lend extra DATA accounts after a 90-second warm grace. Real traffic restores demand. Users can be added, disabled or removed while the server runs without changing other users' profiles or restarting their TCP streams.

Each leased account has two duplex DATA roles; each active user has a temporary duplex CONTROL anchor. Control folders are shared under the server namespace; DATA folders contain the user's UID. Generation-fenced handoffs preserve physical debt. Only the server performs audited cleanup, including recoverable late writes after a phone disconnects.

Update both endpoints together. Preserve profiles, identities and journals; no folder purge or journal reset is required. Physical mail-account credential changes still require a deliberate server restart and refreshed affected client inventories.

Verified locally: race-detected carrier/core suites, five users on twenty-five accounts under an enforced ten-connection provider ceiling, hot registration/revocation, TCP stream integrity through handoffs, variable-latency/network-replacement recovery, native SOCKS/TUN HTTPS and process restart, conservative accounting, late-message cleanup, Android unit tests/lint, release signature and artifact hashes. Local fixtures do not establish a public-provider speed or RTT guarantee; this is a testing candidate.

Assets: signed Android arm64 APK (org.itom.client, code 10303) and static Linux amd64 server binary only. Existing saved signing identity is retained. UDP remains deferred.
