# Receiver credit integration contract

`ReceiveWindow`, `SendWindow` and `ReceiveDemand` are tested state-machine
components. They are **not wired into Session or the record format yet**. They
do not enable automatic window growth, prove network performance or authorize
packaging. This contract completes the credit part of the existing single
structural rewrite; it does not create another intermediate transport release.

## Why a fence is necessary

A large advertised receive window remains permission to send, even when a
stream looks idle. Reusing its backing for another stream without revoking that
permission would overpromise memory. Refusing every later growth request would
instead let old persistent connections starve a new SINGLE transfer. An idle
timer, provider APPEND reply or mailbox proof cannot settle this logical credit.

The receiver requests a new credit epoch with a smaller provisional right edge.
It retains all old backing. The sender switches epoch, stops using old permission
at its current highest dispatched byte, and returns that exact fence watermark.
The smaller provisional grant can still carry short new work immediately. A
duplicate recall returns the original watermark, including after new-epoch DATA
has been dispatched; it never recomputes the old exposure from a later snapshot.

Only a matching authenticated fence lets the receiver release excess backing.
It retains at least the old in-flight watermark minus actual consumed bytes,
and its configured base window. New DATA may overtake the fence response within
the provisional grant. Old delayed credit must not reopen the returned window.
All quantities here are logical byte/memory credit, never storage quota refunds.

## Session and wire requirements before activation

* Bind window scope to the authenticated Noise session, record generation,
  stream ID and direction. Initialize the credit epoch once for the new flow.
  A window component must replace the existing flow-credit fields under the
  session lock, not run beside them as a competing admission authority.
* Carry the feedback credit epoch in authenticated DATA piggyback and standalone
  credit, and add scoped recall/fence protocol records. Stale credit is ignored;
  valid cumulative delivery acknowledgment remains independently useful. No
  per-DATA send epoch is needed: the original sender watermark and provisional
  edge bound every byte which can still arrive during the transition.
* Update the unreleased record codec/version and all exact size-admission paths
  together. Keep original envelope, nonce/replay, grant, body and response limits.
  Retrying an unstarted recall must retain its exact epoch and edge. Coalescing
  cannot replace one epoch with another under the same pending publication.
* Preserve the original complete-body replay validation boundary. Scope, credit
  version and record semantics must validate before replay state is committed.
  Fence processing and existing-stream credit updates belong to the bounded
  capacity-free protocol path; they cannot wait behind DATA admission.
* A fence watermark includes every previously dispatched logical byte, including
  definitely unstarted and uncertain physical publications. Provider acceptance,
  own FETCH and physical deletion do not stand in for a sender fence. Unknown
  commands retain their original recovery and charged debt semantics.
* Feed highest authenticated DATA end, including reordered data, into observation.
  Advance consumption only from actual contiguous application reads. Change the
  session-wide reserved-memory total atomically with growth, fence acceptance
  and later consumption before issuing the resulting credit. Lost recall/fence
  replies retain conservative old backing and use bounded explicit recovery.

## Growth and reclamation policy

Consumption demand requests prompt bounded growth without waiting for a body to
fill. Sparse requests cannot accumulate a lifetime byte count into a permanent
bulk classification. This demand signal is not a bandwidth estimate: validated
peer delivery and measured service must still supply the bandwidth/feedback
model, while the session allocator enforces actual available memory.

Reserve backed base credit for new short work. For example, the current default
128 streams at a 256KiB initial window require 32MiB of the 64MiB session budget;
the remainder can support larger active windows. This is budget geometry, not a
hardcoded network latency or a new quota. Credit epochs allow previously grown
idle streams to return backing when another active flow needs it.

Choose recall candidates using real idle/demand evidence, and avoid shrinking
healthy busy flows merely because loaded RTT rose. A busy-flow race is safe in
the state machine but can cost throughput; its scheduling policy must be measured.
There must be no periodic pool-wide recall storm or fixed placement/fill grace.

## Required integrated cases

In addition to the original release contract, exercise persistent streams which
grew large windows and then became idle, followed by a new SINGLE transfer.
Reorder old credit, recall, fence, old in-flight DATA and new short DATA; lose a
control reply; duplicate recovery controls; and close one flow during recall.
The sender's entire remaining permission must stay backed after every step.
Other streams must keep progressing without socket replacement, quota change,
extra short-request grant round trips or idle-tail spikes.

State-machine tests include 100 randomized control schedules, but cannot satisfy
the full 250ms/2000ms/100Mbps application gates. Wire integration, shared-budget
arbitration, multi-account scheduling, calibrated processes and Android validation
remain required before activation.
