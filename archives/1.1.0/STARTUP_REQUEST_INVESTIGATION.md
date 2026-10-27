# RC1.3 connection failure: request exchange investigation

Status: diagnostic checkpoint, not a new testing release or a confirmed fix for
the reported field failure. The published RC1.3 assets are unchanged.

## Field evidence received on 27 October 2026

The server trace explicitly identifies RC1.3, successfully selects the fast and
bulk mailboxes, and reaches `waiting_request`. The phone reports
`V2_STARTUP_RENDEZVOUS` even after reinstallation. This establishes that the
server completed inventory and published or recovered a standing rendezvous.
It does not establish that the phone read the same authenticated offer or that
the provider accepted the phone's request keyword. No fresh client diagnostic
export accompanied this report, so its configuration hash, actual SELECT count,
last startup stage and elapsed time have not been compared with the server.

Do not attribute this report to an old installed server: the new trace explicitly
shows `itom-1-1-0-rc1-3`. Do not ask for another reinstall or clear state.

## Confirmed reporting defect and changes

The native startup bridge previously emitted the initial state, then sampled
progress once per second. A failure before the first tick could return without
emitting any later phase. An overlay using the exact published `startup.go`
against the new regression fails immediately with only `PROFILE` retained.

The bridge now joins its heartbeat and flushes its last report before returning.
Client startup reports a bounded, application-defined `FailureReason` before
wrapping the error for gomobile. Provider reply text and private paths remain
excluded. Android allowlists and persists this reason, including reason-only
changes.

Offer discovery remains `RENDEZVOUS`. Opening the offer-bound original wallet is
reported as `WALLET`, and publication of the authenticated request as `REQUEST`.
The dialog no longer presumes that a discovery failure means an old or stopped
server. Wire format, authority, quota accounting, deadlines and retry behavior
are unchanged.

## Verification

- Public client/server TLS fixture: a provider that accepts real LOGIN, SELECT
  and offer discovery but returns tagged NO to the request now reports
  `V2_STARTUP_REQUEST` with `IMAP_TAGGED_REFUSAL`. Both actual SELECTs are retained;
  there is only the original server offer, no invitation or uncharged client
  APPEND. This is a diagnostic regression, not proof that the real provider
  returned that refusal.
- The same race-enabled run passed normal HTTPS startup/restart and provider
  keyword-case-change startup/restart: three top-level public API cases passed.
- Full native race suite passed, including the final-report and cancellation
  regressions; full tunnel race suite passed.
- All 92 Android domain tests passed, including seven focused retained-evidence,
  phase-mapping and private-input tests. Core transport/tunnel and native vet
  checks passed. No new APK or device validation is claimed.

The next causal input is a fresh RC1.3 client diagnostics export after failure,
paired with the server option-13 export from the same attempt. Compare their
configuration identities and selected-line counts first. If those agree, use
the observed failing operation to add a faithful provider regression before
changing the rendezvous protocol. Do not present this checkpoint as fixing the
field connection failure or meeting the latency/bandwidth targets.
