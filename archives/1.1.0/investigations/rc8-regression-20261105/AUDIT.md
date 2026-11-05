# RC8 regression and slow manager investigation

User observations are the acceptance baseline: RC7 survived tests/reconnects,
showed 38.6/77.2 Mbps download and 4.17/7.59 Mbps upload with 198/173 ms idle RTT.
RC8 screenshots show 17.6/14.8 Mbps download, 1.35/4.57 Mbps upload, 260/250 ms
idle RTT and approximately 924/930 ms upload-loaded RTT. Stop/update operations
are reported to take minutes. Uploaded documents are measurements, not commands.

## What the captures establish

The RC8 session stays alive and all 30 roles stay available, with no account
failures or local stream-capacity refusals in the current run. Capacity admission
was not this run's bottleneck. Client retransmissions reach 221 and server
retransmissions 134. During download the server's pending queue reaches roughly
18 MiB while the measured flight window is already shrinking. During upload,
client pending bytes reach roughly 13 MiB, flight grows over 4 MiB, then the
window drops back to 512 KiB. Previously queued work and retries do not disappear
when the window shrinks. This is a materially different workload from the clean
slow-receipt growth model used to qualify RC8.

The server cleanup state remains at maintenance_expunge with completed=32 for
several minutes. This is consistent with the decoder deadlock reproduced below;
there is no server goroutine dump in the upload to prove that exact stack in
this particular process. A provider stall alone should have been interrupted
by the operation timeout. Remaining stuck despite that timeout exposes a local
shutdown/cancellation defect.

## Reproduced defects

The IMAP client has bounded 128-entry streaming response queues. The transport
pipelined UID STORE and EXPUNGE, then waited for STORE before draining EXPUNGE.
IMAP permits unsolicited EXPUNGE during UID STORE. A burst of 300 such replies
before STORE's tagged completion fills the later command's queue and blocks
the decoder. Closing only the TCP socket cannot interrupt a decoder waiting on
a Go channel. Client.Close waits for that decoder, which also blocks account
cleanup and server shutdown. The systemd stop timeout plus update rollback/retry
paths can turn this into long manager waits.

Two deterministic reference checks run against frozen RC8 implementation:
(1) undrained 300-reply EXPUNGE then close; (2) real TLS IMAP with 300 retained
messages, provider removal during UID STORE and complete prefix audit. Both
produce the diagnosed failures. The candidate completes those operations in
roughly 20–25 ms locally. The same helper checks the manager command deadline.
The primitive tests add only tests to the frozen reference; its runtime code is
unchanged. Cold compilation has a separate allowance from the ten-second test
execution deadline.

## Final changes

Restore RC7's measured-rate controller. RC8's 50% demand probe and propagation
headroom changes are reverted. The artificial infinite-capacity receipt formula
which demanded 100 Mbps at 500 ms RTT is removed as performance evidence; it did
not model the field's retained storage, command response ordering or queueing.
Keep RC8's deadline-bounded 64-stream admission and cancellation behavior.

Drain UID STORE and EXPUNGE reply streams concurrently, join both drains, then
consume the pipelined audit result. This preserves the command pipeline while
preventing either bounded response queue from filling unattended.
Explicit client close cancels streaming EXPUNGE, FETCH and LIST delivery as well
as network IO. Decoder failure also releases these waits. Operation deadlines
and parent cancellation trigger this complete abort, rather than only closing
the socket. This preserves finite queues and adds no detached decoder workers.
The local fork provenance and complete file-hash manifest are updated.

Manager stop/install systemctl calls have a 20-second deadline and bounded pipe
wait. The installed unit's stop fallback is ten seconds. A failed stop returns
an error instead of resetting capacity while the service might still be alive.
No profile, key, resolver, mailbox format or automatic DELETE policy changes.

## Validation design

In addition to full normal/race/native/SDK/vet and Android checks, a new whole-path
provider model seeds 300 old messages in every new control folder and emits
EXPUNGE during UID STORE. Two complete upload/download cycles must finish with
all 30 roles, zero failed accounts and no provider connection rejections. Idle/
post RTT and loaded RTT stay bounded. Empty-mailbox fast-path and constrained
7 Mbps cancellation/fresh HTTPS checks remain separate. Every target flag,
including misses, is retained; synthetic throughput is not a live prediction.

An initial retained-history fixture passed nil APPEND options to the memory
provider and failed during setup. That fixture error was corrected before the
frozen candidate; it is not counted as a transport failure or a passing run.
A local full-suite attempt overlapped a final diagnostic-only source edit and
correctly rejected its changing fingerprint; final-source verification is
reported separately. No field performance guarantee is made.

A serial-mutation candidate passed the reliability checks but failed the hosted
second-cycle throughput gate at 69.38 Mbps upload. It was not released. The final
implementation keeps STORE/EXPUNGE/audit pipelining and owns one joined auxiliary
stream drain, so a notification burst cannot block the other command's response.
The direct 300-message cleanup replay completes in roughly 9 ms locally with
this implementation. Final-source throughput results are reported separately.

Final source `a4bb15b3a56b9f8098070eabc71029940682989d` passes hosted throughput, retained-history, constrained-link, normal/race/native/SDK/vet and Android compile/tests/lint gates. Both four-stream uploads exceed 100 Mbps, as do both retained-history uploads/downloads. Six actual-binary startup cycles and manual-stop capacity audits pass. All strict pause/rolling target misses remain in the release metadata and evidence.
