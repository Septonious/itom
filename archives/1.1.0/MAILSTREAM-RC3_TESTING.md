# ITOM 1.1.0 MAILSTREAM-RC3 — startup recovery

Matched experimental Android/server pair. Update both endpoints. Install the APK in place, preserving app data and the existing profile. The Android signing identity is unchanged.

## What changed

The RC2 field bundles show successful authentication followed by a failed startup data-path check while retained-mail recovery was active. A subsequent attempt waited behind slow teardown. These observations motivated a regression with a provider that serializes old-mailbox metadata and CREATE across an account’s sockets. That regression timed out before the fix.

Historical outbound-mail recovery now runs before the affected account advertises readiness. It cannot concurrently block live bootstrap commands on that account. Recovery remains bounded, keeps unverified retention debt charged, and never deletes client mail or unrelated namespaces. Other ready accounts can establish the session while recovery continues.

Session traffic uses authenticated evidence of remote account availability rather than assuming that a local login makes the remote DATA reader ready. Establishment and initial mailbox selection have bounded deadlines. Teardown closes the underlying socket before TLS cleanup, avoiding repeated CloseNotify waits. A dial-only deadline no longer cancels a successfully opened stream ten seconds later. Failed startup diagnostics retain session state and bounded transport events.

The delivery controller and bandwidth settings are unchanged from RC2. Per-account measurements, direct DNS behavior, and the server manager’s option 18 remain available.

## Verification and limits

Verification includes the retained-mail restart regression, uneven account recovery, a TLS shutdown fixture, HTTPS surviving beyond its completed dial deadline, module normal/race tests, native startup/restart checks, Android tests/lint, and constrained-link transfer/cancellation recovery. Published evidence records completed checks and measured results. These are loopback provider models, not a live Rambler certification. A real-world retest is still required.

## Recorded local results

Six native startup/restart cycles succeeded with the compiled release server: 30/30 lines for ten accounts and 45/45 for fifteen. Startup took 2.1–4.9 seconds. Five cycles reported 107–152 ms RTT; the first fifteen-account cycle reported 1160 ms during startup.

The four-stream, shared 7 Mbps model completed upload, deliberate download cancellation and fresh HTTPS recovery, with zero failed accounts or connection rejections. Payload rates were 4.52/4.35 Mbps; post-load RTT was 120–224 ms and loaded maximum was 1609 ms. One initial idle probe reached 539 ms.

The ten-second high-rate model measured 105/221 Mbps with one stream, and 61/34 Mbps with four streams. The four-stream loaded maximum was 2705 ms. These results do not establish the universal 300 ms idle / 2000 ms loaded ceilings or sustained 100 Mbps across workloads. This RC addresses startup reliability; it does not claim those performance goals are complete.

## Installing the server

Upload the matching architecture’s ITOM .bin to /tmp, then choose option 18 in the existing manager or run:

```sh
sudo itom update-from-tmp
```

The updater selects the newest regular .bin containing ITOM (case insensitive), preserves configuration/state, and restarts a running service. Keep the app’s profile and data when installing the matching APK.
