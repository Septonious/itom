# Rev9 development checkpoint

Base: published Rev8 commit 644ad76. The strict 100 Mbps and 100 ms jitter goals remain unverified.

Scope: optional per-account literal IMAP TCP address, preserving logical hostname, SNI and certificate verification; server configuration, manager, Android editing, native configuration and exported profiles must round-trip it. Blank retains DNS. No silent DNS fallback after an explicitly pinned endpoint fails.

The supplied Finland analysis reports 16–18 ms TCP setup, not proven authenticated IMAP operation time or ITOM throughput. No migration or live VPS access is implied. Keep every existing physical account/debt and TLS invariant.

Next: implement pinning end to end, targeted regression coverage, then investigate the retained-state MULTI application timeout and run a bounded final qualification/build. Push intermediary results throughout.
