# ITOM-0.9.1-Rev2

Rev1 field failure: five accounts (a01, a02, a06, a10, a11) did not reach readiness. All authenticated; each received the fast grant but no bulk grant. Five accounts (a04, a05, a07, a08, a09) carried traffic. The screenshot reports 14.8 Mbps download, 13.2 Mbps upload, idle/load ping 332/391/364 ms.

## Changes

- Keep REWORK1 scheduling, delivery, latency control, Android and manager as the baseline.
- When a normal grant is blocked and historical remote debt remains, allow a bounded recovery grant from actual free journal capacity. Recovery bulk grants cover a full 512 KiB physical message and use a smaller ordinal quota, preserving the existing fast/bulk classification and wire protocol.
- Preserve a 512 KiB outbound latency reserve plus a 512 KiB outbound bulk BODY and at least two message slots (eight for a legacy 128-slot profile). Debt-free accounts retain the existing normal admission policy.
- Keep pre-APPEND loss of a selected DATA carrier retryable even before its context is canceled. Failed reservation cancellation remains fatal; unknown APPEND debt is never converted into an uncharged retry.
- Reconcile partially spent old open grants as well as unused grants. Replay durable final usage so unused reservation capacity can be released; DATA debt still requires the original physical proof.
- Give trace headers the correct Rev2 identity and include successful reduced grant admission metrics.
- Preserve existing journal identity, effective 8/12/16 MiB profile, all uncertain debt and wallet history. No state reset or automatic profile expansion.

## Regression evidence

The same ten-account regression fails on unmodified Rev1 with only 10/20 directions. With Rev2, the persistent-ledger regression reaches 20/20. The full TLS IMAP scenario reaches 20/20 and verifies ten concurrent bidirectional TCP echoes. Fresh wallets cannot erase historical reservations. A partially spent old grant recovers its unused reservation without refunding unseen DATA. A completely occupied journal remains blocked rather than manufacturing credit.

The first full core run caught an existing selected-carrier availability race in the mandatory eight-stream outage scenario. A deterministic regression reproduces the fatal pre-APPEND error on Rev1; Rev2 preserves the logical stream and returns the unspent grant. The failed first run remains in the validation evidence. Other final build and test results are recorded in the accompanying validation archive.

## Build

```powershell
python .\ITOM-0.9.1-Rev2-sources.zip all --expect-revision transport-rework1-rev2 --accept-android-licenses
```

Outputs: `dist/itom-linux-amd64`, `dist/itom-android-apk.apk`, plus Linux arm64 and validation reports. APK remains the signed debug application `org.itom.client.dev`, version `0.9.1-Rev2-debug`, versionCode 9081. Use the existing debug signing key for an in-place Android update.

## VPS update

After placing the new binary at `/tmp/itom-linux-amd64`:

```bash
sudo chmod +x /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

The built-in installer replaces `/usr/local/bin/itom`, preserves `/var/lib/itom`, restarts an active service and supports binary rollback on install failure.

## Qualification

Local regression/build validation does not establish 20/20 readiness or 100+ Mbps on the live provider. Live VPS, Android TUN and mobile device validation remain pending. Long performance repeats previously stopped by the user are not restarted for this release.
