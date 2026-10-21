# Retained-capacity response experiment

Build on the bounded short-response search and fresh bulk-epoch evidence reset. The first candidate passed full correctness but only14/20 performance rounds, matching RC2 in that cohort; retained SINGLE upload remained around83Mbps for both. This experiment targets that physical supply limit without restoring RTT-only bulk halving.

The existing remote cleanup period is1500ms. When authenticated journal usage records historical debt, use the existing recovered directional shares to scale this period by the bulk byte headroom still available, with a bounded floor. Fresh profiles, unknown authority, invalid state and unconfigured lanes keep the original cadence. Existing FIFO cleanup admission, fast reservations, exact BODY/deletion proofs, journal/wallet charging and all wire contracts remain authoritative. A timer never reclaims capacity. Local cleanup priority is unchanged.

Compare900ms and1100ms minimum periods on matched RC2 hosts. Both use the same five scenarios and20 full60s rounds per variant, original100Mbps/tail750ms gates, per-second positive duplex, scheduled probes and socket identities. Report every invocation, including failures; prefer the gentler timing if it meets the goals without a serious directional throughput regression.

Server propagation is configurable and defaults to20ms. This timing policy is derived from retained byte headroom, not a hardcoded server network RTT or benchmark speed target.

Current DEV3 source/build artifacts and final1.0.0 remain unchanged until the candidate is accepted. Production deltas against DEV3 are limited to the three bounded-response files, the existing cleanup scheduling call in `lane.go`, and `reclamation_policy.go`; physical authority modules are byte-identical. Full correctness runs on the900ms variant while both timings receive complete matched performance comparisons.
