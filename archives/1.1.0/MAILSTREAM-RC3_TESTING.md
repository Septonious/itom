# ITOM 1.1.0 MAILSTREAM-RC3 — startup recovery

Matched experimental Android/server pair. Update both endpoints. Install the APK in place, preserving app data and the existing profile. The Android signing identity is unchanged.

## What changed

The RC2 field bundles show successful authentication followed by a failed startup data-path check while retained-mail recovery was active. A subsequent attempt waited behind slow teardown. These observations motivated a regression with a provider that serializes old-mailbox metadata and CREATE across an account’s sockets. That regression timed out before the fix.

Historical outbound-mail recovery now runs before the affected account advertises readiness. It cannot concurrently block live bootstrap commands on that account. Recovery remains bounded, keeps unverified retention debt charged, and never deletes client mail or unrelated namespaces. Other ready accounts can establish the session while recovery continues.

Session traffic uses authenticated evidence of remote account availability rather than assuming that a local login makes the remote DATA reader ready. Establishment and initial mailbox selection have bounded deadlines. Teardown closes the underlying socket before TLS cleanup, avoiding repeated CloseNotify waits. A dial-only deadline no longer cancels a successfully opened stream ten seconds later. Failed startup diagnostics retain session state and bounded transport events.

The delivery controller and bandwidth settings are unchanged from RC2. Per-account measurements, direct DNS behavior, and the server manager’s option 18 remain available.

## Verification and limits

Verification includes the retained-mail restart regression, uneven account recovery, a TLS shutdown fixture, HTTPS surviving beyond its completed dial deadline, module normal/race tests, native startup/restart checks, Android tests/lint, and constrained-link transfer/cancellation recovery. Published evidence records completed checks and measured results. These are loopback provider models, not a live Rambler certification. A real-world retest is still required.

## Installing the server

Upload the matching architecture’s ITOM .bin to /tmp, then choose option 18 in the existing manager or run:

```sh
sudo itom update-from-tmp
```

The updater selects the newest regular .bin containing ITOM (case insensitive), preserves configuration/state, and restarts a running service. Keep the app’s profile and data when installing the matching APK.
