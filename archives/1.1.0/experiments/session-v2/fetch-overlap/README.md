# Same-socket FETCH overlap evidence

Parent: 8456731d97b21cc0aa150098f240a93e0d1a520d. The parent is the preceding
independent prototype, not the removed transport or a field-performance oracle.

The final implementation overlaps only singleton BODY and disjoint FLAGS reads
with enough framing headroom. Public FETCH, grouped bulk, inventory, absence
proof, overlapping UIDs and narrow custom budgets stay exclusive. All active
FETCH responses share the original aggregate response bound.

Final checks:
- overlap-final-race-repeated.jsonl: ten targeted groups, three race repeats,
  thirty pass, including grouped bulk and narrow-budget exclusion.
- overlap-core-race.jsonl: 249 final default core race groups pass.
- overlap-native-race.jsonl: ten native race groups pass; both vet outputs empty.
- parent-negative.jsonl: parent fails independent BODY/FLAGS completion, expected.

Application/propagation checks, with all original request deadlines:
- overlap-warm-repeated.jsonl: two complete 64-exchange cohorts, each still FAILS
  300ms once. The 300.177603ms sample remains a violation, not rounded down.
- overlap-upper-and-quiet.jsonl: upper 50/40ms has four misses in sixteen; quiet
  has three completions after real silence and no misses. These are separate gates.
- parent-light-cohorts.jsonl: same current test harness with parent implementation;
  all 83 requests complete. Warm, upper and quiet each fail their maximum.
- overlap-parser-service.jsonl and parent-parser-service.jsonl: twenty pairs on
  one real selected TLS socket over constant 50ms opaque TCP propagation.
  This excludes Noise, journal, account authority and application flow service;
  it is not tunnel RTT, useful Mbps or release qualification.

The earlier overlap-transport-race.jsonl has 208 passes and one failed plain
NOOP calibration at 503.779ms. It does not use FETCH overlap. Both standalone
calibration-repeat files subsequently pass three groups/sixty probes each,
without erasing that earlier failure or proving a sole cause. Earlier smoke
and protocol-stage pass files are retained as development history, not used as
final-source or performance qualification.

To replay the parent implementation, run:

    python3 archives/1.1.0/experiments/session-v2/fetch-overlap/replay-parent.py --output-dir /tmp/itom-parent-fetch

From archives/1.1.0/source/core, add
`-overlay=/tmp/itom-parent-fetch/overlay.json` to the same exact go test command.
The script restores five parent implementation files from Git. It adds only
three zero-valued diagnostic fields to the parent's snapshot TYPE so the same
current diagnostic/test harness compiles. It changes no parent behavior, limit,
clock or timing. New pair counters are zero in that parent variant. Source and
shim hashes are recorded separately in the manifest. The new independent-
completion regression must fail; application and component measurements retain
all their own successes/failures with the overlay, without running that negative
control as part of a qualification cohort.

The workflow adds repeated overlap guards and the observational parser-service
component; existing warm/upper/quiet gates remain strict. No binary or APK is
built, and sustained 100Mbps/production startup remain unqualified.
