# Journal profile upgrade / rollback — field2

The journal wire scope and debt semantics are unchanged. Opening an existing store is not a reset or a capacity upgrade.

| Stored DATA profile | field2 default open | Effective byte/message limit | gate19 rollback |
|---|---|---|---|
| No DB | Create 12/192 | 12 MiB / 192 | Requires audited downgrade first |
| Exact gate19 8/128 + same owner/provider/folders | Accept, retain config | 8 MiB / 128 | Config is compatible; run fencing/recovery still apply |
| Exact field1 12/192 + same scope | Accept | 12 MiB / 192 | Denied until a successful audited downgrade |
| Other limits or owner/provider/folder/metadata caps | Fail closed | None | Investigate state; do not erase DB |
| Corrupt counts/debt/refs/audit | Fail closed | None | No reset fallback |

Control journals stay 2 MiB/128 with strict config equality. Startup logs `journal_profile` with effective bytes/messages and existing/fresh state even when diagnostics are off. Bulk admission and remote offers use the effective saved DATA byte limit minus the existing 512 KiB fast reserve. Pending DATA RAM ceiling remains the field1 bound of 12 MiB.

## Optional offline migration

Ordinary upgrade needs no migration command. Both Android and VPS keep confirmed old 8/128 journals at their saved limits. The following VPS command explicitly opts into 12/192:

```bash
sudo systemctl stop itom
sudo /path/to/field2/itom-linux-amd64 journal-profile --state /var/lib/itom --to=data12
sudo systemctl start itom
```

Exclusive bbolt locks prevent migrating a live DB. Each journal migration is atomic and idempotent, with a version-1 bounded audit record: monotonic sequence, run, from/to config SHA-256, record counts and audited outcome. It checks all old records and reconstructed balances, then checks they fit the proposed profile; writes config and audit in one fsynced transaction. No attempts, debt, IDs, refs, locations/digests or owner scope are rewritten by migration. Reopen advances run and converts orphan Started to Unknown before migration; it never authorizes a stale writer.

A multi-journal directory is not one atomic transaction. If interrupted, some files may already be 12/192 and others 8/128. Field2 accepts both; repeat the command safely. Inspect per-account/user results and exit code. It skips nonexistent configured journals rather than creating fresh state. Orphan directories for accounts/users no longer in server.db are not migrated.

Android has the same compatible opener and journal migration primitive; this revision deliberately adds no new device/UI migration flow. Existing Android 8/128 state remains transitional until an explicitly designed device migration is qualified.

## Downgrade

Stop the service and use the same command with `--to=data8`. The operation rejects any held byte/message exposure or still-live grant whose original limits do not fit 8/128. It also rejects foreign scopes and corrupted accounting. The rejection does not reset state or refund debt. Keep field2 or another binary that accepts the effective saved profile, let physical proof retire the incompatible records, then retry offline.

Gate19 understands only exact 8/128 and field1 only exact 12/192. Restoring an old DB snapshot after new network activity can forget Started/Unknown APPENDs and is not a rollback procedure. Do not delete journals/mailboxes to make an upgrade pass.

Process-death regressions exercise termination before commit, after durable commit, and lost commit acknowledgement; they do not emulate power loss or broken fsync hardware. Real deployment/installer/device upgrade qualification remains pending.
