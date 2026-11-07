# Wi-Fi/mobile field comparison — 15 October 2026

The four new client captures show two distinct problems. Wi-Fi bulk mailbox bodies arrive much more slowly than mobile bodies, even though Wi-Fi control commands are generally faster. Account `a05` remains authenticated and responsive to control traffic but never receives usable storage grants on either network. The client evidence does not establish the ISP as the pacing source, or establish the exact server-side quota reason for `a05`.

## What was examined

All three rolling trace files in each supplied ZIP were parsed, deduplicated by run/sequence and correlated by run/account/operation. There are 472,841 retained events in four separate runs, with no cross-capture overlap. These are rolling tails, not complete startup recordings. The small diagnostics snapshots were exported after the sessions stopped and show OFF; the trace events provide the during-run state. No attachment text was treated as an instruction.

The table includes operations that actually delivered at least 32 KiB of decoded mailbox-body bytes. These are FETCH operation durations, not end-to-end Speedtest results or pure network RTT. A page can contain multiple messages. Times below are Moscow time (UTC+3); exact source timestamps in the JSON evidence remain explicitly UTC.

| Capture | Retained interval, MSK | Bulk BODY operations | Median seconds | p95 seconds | Maximum seconds |
| --- | --- | ---: | ---: | ---: | ---: |
| wifi | 11:24:53–11:26:51 | 91 | 0.957 | 3.275 | 6.036 |
| mobile | 11:27:50–11:30:00 | 482 | 0.120 | 0.333 | 0.585 |
| wifi2 | 11:58:59–12:00:23 | 85 | 2.288 | 8.122 | 16.128 |
| mobile2 | 12:02:11–12:04:00 | 170 | 0.165 | 0.545 | 0.721 |

## The Wi-Fi slowdown is in the BODY transfer

In wifi2, account a15 operation 97568 runs from 11:59:29.051 to 11:59:45.179 MSK. Its 66,114-byte first decoded BODY arrives after 2.539 seconds; its 362,646-byte next BODY arrives after 16.128 seconds. Completion follows the final BODY by 0.049 ms. Both bodies preserve their original UID/operation association.

Concurrent control operations sample the same DATA receive socket, ID 30. Its outgoing byte counter stays at 182,048 while incoming bytes climb from 2,216,590 to 2,807,155 over 16.076 seconds: approximately 294 kbit/s. Incoming progress remains frequent (sampled socket idle ages 0–155 ms). This is an in-progress physical receive, not a 16-second pause after the last BODY or a socket already known dead. Callback completion at the last BODY is also immediate in the representative operations. These observations separate continuous slow transfer from an ITOM actor queue stall.

Multiple other active DATA sockets in the same interval show approximately 0.23–0.294 Mbit/s each; the sampled aggregate is about 3.276 Mbit/s before MIME/TLS/application overhead. That is consistent with the reported low useful Wi-Fi download speeds. The exact source of pacing remains open: Wi-Fi/router behavior, transit/ISP policy, mail-provider policy for that source IP/path, or TCP behavior need a direct path comparison to separate. The captures do not prove a global ITOM 5 Mbit/s limit.

Wi-Fi median control FETCH duration is approximately 26–30 ms versus 42–44 ms on mobile; median DATA APPEND is approximately 73 ms versus 88–125 ms. Thus higher general RTT is not an adequate explanation for Wi-Fi's large-body delay. A 362,646-byte BODY on mobile2 completes in 0.721 seconds in the slowest sampled mobile2 bulk operation. Screenshots cannot be mapped one-to-one to run, SINGLE/MULTI mode or chosen Speedtest server from these captures alone.

The receive guard intentionally allows a long BODY while the socket continues making progress, with a separate absolute operation cap. Treating this paced socket as dead would create repeated downloads/reconnections. Adding many TCP sockets without measuring the provider's session policy is not a demonstrated repair.

## Account a05 is stuck at grants, not TLS/LOGIN

Every available a05 health sample in every capture has Stage=grants, AvailableGrants=0, DataAttempts=0, and an empty LastError. The other fourteen accounts reach Stage=ready. a05 continues receiving authenticated control traffic and reporting application RTT, including 618 ms / 766 ms / 531 ms / 920 ms at the last mobile/mobile2/wifi/wifi2 snapshots. It is therefore not simply an offline account or a failed TLS login in these traces.

Old-grant reconciliation is refused with WALLET_HISTORY_MISSING on all fifteen accounts. Counts on a05 are 81 / 63 / 67 / 54 in mobile/mobile2/wifi/wifi2; other ready accounts also report missing history. This means the current wallet cannot prove the spend history of those old grant sessions. It does not alone prove that a05's ledger is full. A full or otherwise constrained server ledger with retained old reservations is a plausible cause of zero new grants; the matching server held/limit/issued/closed/released accounting is required to establish that cause.

The previously pasted atmoslink6 provider reply, NO [SERVERBUG] Mailbox uidvalidity changed, is a folder/selection failure. These new client exports contain account IDs, not an ID-to-email table, and do not establish that atmoslink6 maps to a05. Do not infer an identity match solely from their order in a UI list. UIDVALIDITY changes must invalidate old physical locators; they are not proof that any uncertain quota can be released.

WALLET_EXHAUSTED on healthy lanes is not sufficient evidence that the account is permanently exhausted: the cumulative lease writer can reject an exact next spend, fence that lease and continue on a replacement. Those traces must be distinguished from a05, which has no new usable grants and no DATA attempts at all.

## Checks and remaining work

Six relevant existing top-level tests were run again, producing ten passing test events including TLS/STARTTLS subcases, zero failures and zero skips: missing/foreign wallet history, full retained quota, partially spent grant reconciliation, slow-literal service policy, independent no-progress receive repair, and verified append/fetch/delete. These checks support the recovery rules; they do not recreate the user's server ledger or qualify this field path for 90 Mbit/s. The previously completed full internal qualification remains evidence for the unchanged testing build.

No production source or release assets changed during this comparison. Qualification source fingerprint remains ec20fb8f08dd65552ffb2db9f328d306b90178e01d28e5a987c46dbbb7f74da8. The current APK/binaries remain the explicitly unstable testing1 package.

To choose and verify a repair for a05, the next required input is a current server diagnostic export from the same deployment, including per-account quota/grant accounting and account-ID mapping. The earlier server export predates these tests. No password, wallet deletion, journal reset, mailbox purge or invented zero-spend acknowledgement is needed for this analysis. A repair must preserve existing charged debt until the recorded physical/spend proofs justify release.

The next field-derived test scenarios should distinguish (1) low downstream bandwidth per IMAP TCP connection with fast control/upload and intact continuous BODY progress; (2) insufficient free server quota with a client wallet that lacks the old session; and (3) an actual provider UIDVALIDITY setup error. These are distinct failure mechanisms and should not share a blind reconnect or quota-reset remedy. They are proposed scenarios here, not newly implemented test claims.
