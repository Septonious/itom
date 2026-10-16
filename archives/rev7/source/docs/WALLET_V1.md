# Client grant wallet — imap-1-manager-1

`wallet.Open(path,user,serverStoreID)` opens an exclusively locked bbolt store,
checks identity/schema/descriptor accounting and generates a fresh random
session ID. The future Noise binding must use Session(). All earlier grants
are fenced on every reopen, including a graceful restart. There is no replay
of old I/O or TCP payload. Server store identity must come from an authenticated
startup contract; this wallet is not itself a handshake/authentication layer.

Accept consumes an authenticated immutable StorageGrant. Duplicate acceptance
preserves lifetime Used. A conflicting limit/key is rejected. Only current
session offers may be first accepted. Persistent grant-ID high watermark blocks
resurrection of older retired offers. Out-of-order lower-ID offers are refused
conservatively; the actor must reconcile them with the coordinator rather than
silently assume their storage was returned.

Spend constructs the ITQ1 physical wrapper, computes its full digest and MIME
charge, atomically increments Used and saves the descriptor before returning
body + report. Successful return is the ONE I/O permission; resending the
report is not permission for another APPEND. A lost commit acknowledgement
returns no body or report and poisons the wallet until reopen. Each retry via
Spend is a fresh charged attempt. The size callback is trusted local pure MIME
sizing, never untrusted code or network work. Payload bytes are not persisted.

Fence commits a permanent admission stop and returns immutable final usage.
Every previously returned I/O permission is included even if not yet completed.
Pending/uncertain attempts therefore cannot be hidden in the unused release.

Acknowledge accepts only authenticated coordinator GC_STATUS. It validates the
reported prefix against stored cumulative descriptors before pruning metadata.
It never replenishes Used. A fenced grant can be retired only with Closed and
matching final Used/Released. Persistent high watermark still rejects a replay.
A delayed unchanged registered prefix with a new final cleanup acknowledgement
also retires correctly. ErrMissing from the coordinator is NOT cleanup proof.

Bounds: 128 active grants and 64 unacknowledged descriptors per grant, metadata
only. Reaching a cap stops new work. Individual records are rewritten as bounded
JSON values in bbolt transactions; fsync stays enabled. Production batching and
performance tuning need measurements before relaxing commit granularity.

Outstanding integration: durable coordinator retirement status after its final
row is removed (lost final GC ACK), out-of-order offer reconciliation, store-ID
binding/rotation and explicit operator migration, generation-safe actor jobs,
real DATA sender ownership and end-to-end grant/recovery faults. The wallet
currently provides no permission to bypass connection or provider rate limits.
