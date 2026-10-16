# Rev9 development checkpoint

Base: published Rev8 commit 644ad76. The strict 100 Mbps and 100 ms jitter goals remain unverified.

Scope: optional per-account literal IMAP TCP address, preserving logical hostname, SNI and certificate verification; server configuration, manager, Android editing, native configuration and exported profiles must round-trip it. Blank retains DNS. No silent DNS fallback after an explicitly pinned endpoint fails.

The supplied Finland analysis reports 16–18 ms TCP setup, not proven authenticated IMAP operation time or ITOM throughput. No migration or live VPS access is implied. Keep every existing physical account/debt and TLS invariant.

Next: implement pinning end to end, targeted regression coverage, then investigate the retained-state MULTI application timeout and run a bounded final qualification/build. Push intermediary results throughout.

## Pinning checkpoint

Implemented optional `dial_ip` end to end: literal IPv4/IPv6 validation, TCP destination override while retaining TLS hostname verification, server manager account/all-account configuration, authenticated guarded admin mutation/status, Android account editing and native bridge, and invitation/profile export. Clearing a pin restores DNS; account IDs and journals are retained. Profile edits also preserve an existing STARTTLS setting.

Targeted Go race checks passed for pinned TLS/STARTTLS, strict certificate rejection, one-login persistent reuse including idle NOOP, profile export/clear and manager configuration. Android compilation and guarded mutation rollback verification remain pending at this checkpoint.

Audit: the inherited client already maintains idle auxiliary connections using NOOP after 20 seconds without socket I/O, checks every ten seconds, and skips occupied command/IDLE slots. Existing dedicated readers use IDLE; no per-packet LOGIN redesign is needed. The new route report does not establish per-command latency, so the low-RTT laboratory case is a scenario, not a measured Finnish-provider certification.
