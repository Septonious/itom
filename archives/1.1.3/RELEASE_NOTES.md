# ITOM 1.1.3 — unpublished candidate

Qualification is in progress. This source is not a published or field-verified
release and does not establish a live-network throughput or RTT guarantee.

The controller retains its learned full-body DATA path when pacing temporarily
stops it from filling the flight allowance. Slow control callbacks must persist
across separate receipt intervals before bulk protection activates; severe delay
still activates protection immediately. Initial flight, memory and mailbox
limits remain unchanged.

Native diagnostics include existing cumulative TCP retransmission, timeout,
packet-send-error and TUN-drop counters. Reading those atomic counters adds no
sampling timer, packet capture or profiling worker to the phone.

Mailbox mutation remains exclusively server-owned. Stops, updates, abrupt phone
disconnects and restarts preserve uncollected debt. No reset, cap increase,
cleanup command change, legacy purge or new transport feature is included.

The new qualification frontend exercises the actual native TUN/TCP path and
timed download cancellation before upload. Generated provider models complement
the existing regression suite; they cannot replace a test on the affected phone.
