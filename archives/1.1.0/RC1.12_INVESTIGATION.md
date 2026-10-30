# RC1.12 startup investigation

The matching RC1.11 phone and server exports show different startup positions.
The phone selected 20/20 IMAP sockets and remained at REQUEST after 53 seconds;
the server made four starts, three of which failed in retained inventory before
rendezvous. The primary errors were LOCAL_OR_SESSION_FAILURE. Older RC7/RC9
events in the combined phone export are not evidence about this RC1.11 attempt.
The configuration hashes match. The original logs do not expose the underlying
error, so the exact field cause remains unconfirmed.

The released RC1.11 amd64 executable was tested without a client against a TLS
IMAP fixture containing an authenticated original local BODY at a different UID
from its original journal locator. It exited before rendezvous with JOURNAL_STATE,
reported as LOCAL_OR_SESSION_FAILURE. Separate tests reproduce duplicate copies
under two UIDs. These reproduce real startup defects with the observed error
class; they do not establish that the provider made this exact change in the
uploaded run.

RC1.12 completes full authenticated bounded inventory while excluding conflicting
historical cleanup proofs. It preserves the original locator and charged physical
debt. For duplicate references, every copy is excluded from cleanup, including
the first candidate. Failure of optional cleanup after completed authenticated
inventory no longer blocks the originally planned service connection. This never
authorizes deletion or refunds from missing history, peer acknowledgement alone,
uncertain writes or a closed socket. Incomplete or unauthenticated inventory,
cancellation, journal corruption, poisoned state and lost journal ownership stay
fatal.

Passive snapshots record inventory phase, fixed error class, observed-body and
proof-conflict counts, and selected IMAP command/close state. They issue no mailbox
probes and change no network deadline. No provider text, message contents, UID,
key or password is added to these records. Wrapped CONNECT/LOGIN errors retain
their original phase instead of being replaced by a generic socket reason.

Validation includes before/after proof-conflict tests, completed-inventory cleanup
refusal and lost-tag tests, corrupt/poisoned/reowned journal rejection, HTTPS with
conflicting history, persistent wallet restarts, and execution of packaged server
artifacts. The Android package is checked for identity, persistent signing key
and native-library hashes; Linux native bridge tests do not constitute execution
of the APK on an Android device.

An intermediate full CI run passed 318 of 319 core groups. The remaining restart
fixture treated an APPEND interrupted by its own intentional shutdown as a live
failure. Its sockets now pin the owner's stop generation, as the existing native
fixture already did. Twenty repeats passed after that test-only correction.
Incomplete APPENDs before the declared owner stop are still errors. The complete
suite passed for the final source: 319 core groups and 14 native groups under
the race detector, with both modules vetted. Android checks passed 93 domain
tests and eight app unit tests, with no lint errors. The failed intermediate
run remains in the evidence.

The final artifacts passed seven test groups: 20 persistent restarts, fresh
startup, historical locator conflicts, legacy debt, a retained 128-message
backlog, native administration through the packaged server, and native DNS plus
SOCKS/TUN HTTPS through the source fixture exit. The APK's two native libraries
match the build report, and its signer matches the preceding release. These
checks retain their exact scope; APK execution on Android and Rambler access
were not available in this environment.

This release repairs reproduced startup defects and improves diagnosis of any
remaining field failure. It does not qualify Rambler operation, sustained 100 Mbps
or the full 300 ms RTT limit. Raw source performance results, including failures,
are retained in the synthetic evidence. Uploaded private diagnostics and signing
material are excluded from public assets. Update both endpoints in place and
preserve profiles, wallets and journals.
