# RC13 repeated home-network tests

Field results: first 118/62.8 Mbps down/up, idle 154 ms, loaded 241/263 ms;
second 69.4/11.2 Mbps, idle 159 ms, loaded 226/182 ms. Latency stays low
while throughput falls, especially upload. Both captures retain older trace
rotations; this investigation filters strictly to the current RC13 revision.

The current client keeps 30/30 roles and ten ready accounts. Initial recovered
debt is 533,078,096 bytes. By the final sample, nine accounts have about 66.7 MB
charged against their 67.1 MB caps. Account a08 publishes no DATA at all; a06
delivers about 56 MB upload and remains the only account with substantial space.
Only two client DATA segments have been reclaimed. This is storage exhaustion
with functioning carriers, rather than high baseline RTT or lost logins.

The server starts with zero local debt, then reaches about 445.2 MB. It quarantines
nine accounts after repeated `maintenance_expunge` or `store_expunge` timeouts;
only a06 completes control collection (2,121 messages). The client still reports
about 1,300 outstanding controls on each affected account. APPEND completion is
usually 45–108 ms, while NOOP is about 9–31 ms. The grouped mutation stage does
not reveal whether STORE or EXPUNGE stalls; the captures do not prove a provider
pipeline defect or an exact provider mutation duration.

The implementation gave an entire inventory/STORE/EXPUNGE/audit only two seconds,
and attempted the full consumed control prefix at once. A folder with roughly
1,000–1,500 controls repeatedly retries the same large operation after abort.
The candidate inventories at most 128 sequence positions, deletes only UIDs
within the authenticated consumed prefix, and audits that bounded prefix before
advancing credit. Historical folders advance only after all pages prove emptiness.
The global mutation lease and all physical byte caps remain unchanged.

Mutation commands now have tagged completion fences: STORE is fully drained
before EXPUNGE is issued, followed by fresh SELECT proof. Optional collection
has an eight-second hard bound independent of NOOP/APPEND timing. The borrowed
writer remains excluded from dispatch while other accounts carry traffic. No
reader is closed for cleanup, no extra connection is introduced, and cancellation
still interrupts raw IO. Optional cleanup yields to already accepted control
packets before excluding its writer from new dispatch. Unknown outcomes remain charged and quarantined.

New tests preserve unread/future controls during paged deletion, require every
historical page before emptiness proof, reproduce a useful 2.4-second mutation
being discarded by frozen RC13, and preserve the >128 early-EXPUNGE decoder
regression. The provider model adds per-row mutation cost, independent of RTT
and APPEND cost, plus repeated complete speedtests with retained control history.
These model costs are explicit stress hypotheses; real provider behavior still
requires field confirmation. Raw account captures remain private.
