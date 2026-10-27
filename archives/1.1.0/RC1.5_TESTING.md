# ITOM 1.1.0 RC1.5 — short-keyword startup and receipt compatibility

## Install both endpoints

Update the app in place and install the matching RC1.5 server binary. Launch
the newly downloaded executable, choose option 11, then option 9. Choosing 11
from an older running executable reinstalls that older executable. Verify the
actual service executable reports `itom-1-1-0-rc1-5`.

Keep the existing profile, app data, wallets and server state. No profile
re-import, reinstall, new namespace or quota reset is required. Both endpoints
need RC1.5 when the provider refuses the legacy request keyword: older servers
cannot assemble the shorter representations. The Android signer is unchanged.

## Evidence from the paired field logs

Both RC1.4 phone attempts selected all 20 lines, matched the RC1.3 server's
configuration hash, authenticated its offer and opened the wallet. The provider
then rejected the request's UID STORE with `IMAP_TAGGED_REFUSAL`. The attempts
failed at 976 ms and 890 ms, while the server remained at `waiting_request`.
This was a provider command refusal, not failed login, a missing server offer or
a mismatched profile. The logs do not include the provider's reason text, so its
exact keyword-name limit is not established.

The legacy request uses one 103-character keyword. Synthetic providers that
advertise permanent keywords but reject names above 64, 32 or 16 characters
reproduce the same request failure. The previous single-keyword submission
fails this regression before any HTTPS session starts.

## Changes

- A tagged request refusal can select bounded representations with maximum
  keyword lengths of 59, 31 or 16 characters. The complete original Session ID
  and full 256-bit MAC are preserved. No timeout/unknown outcome triggers this
  fallback, and no uncharged HELLO message is introduced.
- Servers assemble complete, unambiguous, case-equivalent fragments and still
  validate the original full MAC before original journal admission. Authenticated
  hints are removed after the invitation is published; metadata removal never
  deletes mail or refunds storage. Partial/conflicting/forged hints cannot grant
  capacity or trigger an authenticated VPN restart.
- The same limitation affected 71-character delivery receipts. Receipts now use
  the accepted representation and can fall back on their own explicit refusals.
  Receipt matching respects IMAP keyword case-insensitivity. All MAC bits, full
  BODY observation, exact UID expunge and independent absence proof remain
  required before releasing original physical debt.
- A longer restart regression exposed delayed aggregate/fence snapshots for
  already retired grant metadata. Persistent reconciliation now drops only
  obsolete aggregate references below the original durable allocation bound,
  without issuing a status, granting capacity or changing balances. Missing
  physical descriptors and unknown physical bodies still fail closed; future
  unallocated grant IDs are rejected. No missing history becomes zero spend.
- Startup exports record `RequestKeywordBytes`. Server failure traces retain
  the final runtime snapshot and fixed journal failure category.

## Validation and remaining limits

Local regressions pass all three provider name limits, full MAC/physical-location
checks, and four real TLS/HTTPS VPN sessions with 10 accounts / 20 selected lines
and 20/40/30 ms command service variation. A separate safety run passed 25 test
groups; both Go vet checks and the complete native race suite passed. Existing
application startup, IMAP, OPEN, TLS and HTTP deadlines were not increased. Only
the test cohort's outer deadline scales with its number of restarts.

Intermediate failures are retained in synthetic-only evidence: request-only
handling first exposed disabled long receipts and mailbox exhaustion; the
longer cohort then exposed the retired aggregate-control race. These were fixed
before the final local regression passed. No user diagnostics are published.

This is a testing RC. The field data proves command rejection, but a real Rambler
run is still needed to confirm it accepts the shorter forms. There is no claim
of sustained 100 Mbps or completed idle/loaded RTT qualification. If connection
still fails, export the new client and server diagnostics; the client trace will
show the smallest attempted representation without exposing provider reply text.

The exact packaged source passed 275 core race-test groups, 13 native race-test
groups, both Go vet checks, source/launcher checks, 92 Android domain tests and
8 app unit tests. Android compilation and lint passed with zero errors and one
dependency-version warning. APK v3 signing and the unchanged persistent signer
were verified. All 701 tracked source files in the source archive match the
build commit byte-for-byte.
