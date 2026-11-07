# Source22: fast admission reservation and independent wake

Source21 reservation fix is retained. Local fast journal admission now waits on the separate fastWake channel, like remote fast admission. An ordinary bulk waiter cannot consume its only wake while global fast priority temporarily blocks bulk. A deterministic regression test signals only fastWake after establishing that the selection is waiting with no carriers; the previous wake policy times out. Priority still ends at physical reservation, not APPEND completion. No quota, timeout, zero-loss acceptance or lease-size changes.

Final status and exact-source evidence belong to the accompanying report. Source20 RTT FAIL remains historical and is not relabelled.
