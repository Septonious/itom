# ITOM 1.1.5 testing — critical idle-writer recovery

The supplied 1.1.4 traces show seven client accounts and one server account permanently retired at writer_keepalive after the speedtest-to-video transition. The external reason for the provider keepalive failures is not recorded by those traces; the permanent retirement behavior is a confirmed transport defect.

Idle-writer EOFs and bounded NOOP failures now replace the APPEND writer pair with shared login throttling and exponential retry. Selected control/DATA readers, authenticated sessions, generation counts, delivery state and all unaudited mailbox debt survive. A NOOP runs only at an APPEND fence, and new application APPENDs wait for its outcome. Uncertain application APPEND errors keep their existing fail-closed behavior; this patch does not guess whether an unknown literal was stored.

Four connections/account and the existing folder layout remain unchanged. Merging folders would not repair these writer failures and would require a separate cleanup/protocol change. No migration, purge, quota reset, cleanup permission or controller change is included. Cleanup remains server-only.

The baseline has ongoing provider EXPUNGE timeouts and inherited debt. Those remain charged and bounded; writer reconnection is not deletion proof.

Use the matched APK and server binary. This APK uses the saved 1.1.3/1.1.4 signing key and updates in place. Two active users still require sixteen provider connections/account; the ten-connection default admits one active user.
