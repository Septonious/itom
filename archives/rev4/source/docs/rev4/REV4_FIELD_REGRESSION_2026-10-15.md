# Rev4 field regression: unresolved candidate

The published Rev4 laboratory qualification did not predict the supplied field
results. Treat that release and this unfinished repair as unstable. This note
does not qualify a replacement APK or server. Every required internal test must
pass before publication; the user's 10% speed/RTT tolerance remains unchanged.

## What the new captures establish

The two Android traces are different sessions on 2026-10-15: approximately
01:19:09–01:22:13 UTC and 01:23:43–01:26:36 UTC. The server's retained trace
overlaps only the second session. Both Android captures identify Rev4. They end
with explicit session shutdown, rather than a recorded fatal process stack.
The separate report that the whole app closes when connecting over mobile data
therefore remains an additional, unconfirmed crash investigation.

Most accounts remain connected during the failed throughput periods. Successful
APPENDs are commonly tens of milliseconds, while useful DATA remains outstanding
for tens of seconds and the delivery window repeatedly reaches 64 KiB. Matching
sampled physical packet hashes with their first receiver observation finds
large upload packets delayed by as much as 97 seconds, versus subsecond control
observations. The receiving operation is the ordinary DATA page reader. Trace
sampling and unsynchronized clocks prevent a complete loss inventory or exact
one-way latency claims. Account names, hashes, raw traces and credentials are
not included here.

## Confirmed reproduction and repair

The dedicated DATA reader starts with a zero cursor. After normal APPEND and
EXPUNGE, an empty mailbox retains its high UIDNEXT. The reader fetched four UID
numbers and advanced across absent allocated UIDs only when its two-second
SELECT audit ran. New DATA at UID 400 could consequently wait minutes behind
deleted historical UIDs, although controls in another mailbox worked promptly.

The new real TLS/IMAP test appends and physically deletes 64 messages, appends
a new 32 KiB DATA payload and invokes the actual split DATA reader. Published
Rev4 fails its 1,100 ms deadline with cursor still at UID 4. The candidate
locates the live message using a bounded UID SEARCH and bounded exact BODY
fetch. Cached allocation observations also permit ordinary forward progress
between audits. Neither discovery nor SEARCH absence supplies deletion or quota
refund evidence.

Skipping sparse spans retains compressed, expiring discovery intervals. Delayed
index/BODY visibility must still be rediscovered after a reader socket handoff,
without extending the original discovery lifetime or reusing another
UIDVALIDITY. SEARCH response size, expanded UID count, BODY batch size and
discovery metadata remain bounded.

## Why the former tests missed it

The retained-state application fixture reserved server journal capacity but
used fresh physical mailboxes with UIDNEXT=1. It did not reproduce installed
mailbox allocation history. Retained-state application tests now seed real
APPEND/EXPUNGE history in both DATA directions. Speed qualification also records
and seeds physical UID history by default, while keeping the existing useful
speed and loaded RTT requirements. An old test that asserted four-UID cursor
increments now asserts immediate delivery of the live tail instead.

## Mobile app crash status

Connectivity callbacks execute outside runSession's exception handler. Platform
exceptions could escape those callbacks and terminate the app. The candidate
contains ordinary callback failures and cancels the owning session for normal
cleanup, rejects stale generation callbacks, and tests concurrent failure.
Foreground startup and native cleanup exception paths are handled separately.
These are demonstrated exception escape paths, not a proven explanation of
the reported phone crash. A fatal Android/native stack or process-exit reason
is still needed to identify that crash. No physical Android device or real
mobile provider has been tested in this environment.
