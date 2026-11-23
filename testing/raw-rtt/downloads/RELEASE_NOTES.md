# ITOM Raw RTT

This standalone diagnostic measures the round trip for an actual IMAP-carried payload from an Android device to an Ubuntu echo peer. It opens a raw test session over TLS through one mailbox. The Android VPN stays disconnected.

## Run one test

1. Install **ITOM Raw RTT** on Android 8 or newer.
2. On the Ubuntu server, run the peer with the host and login for one of your IMAP accounts. Enter the account password at the terminal prompt.

   ```sh
   chmod +x itom-raw-rtt-peer-linux-amd64
   ./itom-raw-rtt-peer-linux-amd64 -imap imap.rambler.ru -user 'YOUR_MAILBOX'
   ```

3. Paste the peer's invitation into the Android app. Enter the same mailbox password and tap **Run test** with the VPN disconnected. Tap **Copy results** when it finishes.

The peer uses port 993 by default. Use `-port` for a different IMAP TLS port. Start a fresh peer for each test. The peer creates two temporary folders and removes them at the end of the test or when its deadline expires.

## What the test measures

The phone times each payload from the request APPEND to reception of the authenticated echo body. It uses one monotonic clock. Independent FETCH connections can see new mail while the APPEND reply is still outstanding. The peer waits briefly between empty reads, using a one-millisecond pause once the warm-up packet arrives.

Setup and warm-up are shown separately. The timed run uses eight requests per size for 64, 512, 1,400, 4,096, 16,384 and 49,152 bytes. Only one packet is in flight. A reply timeout ends the test and appears in the copyable report. The report shows raw samples, min, median, p95 and max.

Local tests run the Android protocol code against a real TLS IMAP fixture and the Go peer. They cover synchronizing and non-synchronizing IMAP literals, the timed echo arriving before a delayed APPEND tag, authenticated payload integrity and bounded folder cleanup. The fixture timings describe the local test only.
