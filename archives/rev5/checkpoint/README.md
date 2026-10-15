# Rev5 unstable development checkpoint

This is a recovery snapshot requested during session network trouble. No Rev5 release is authorized by this snapshot: full current-source qualification is incomplete.

- `../source/` contains the exact editable source, tests and build tools.
- `STATUS.json` records implemented work, pending work and acceptance limits.
- `CURRENT_HANDOFF.md` contains the detailed resume instructions and historical diagnoses. Its newest block overrides older status statements.
- `drivers/` preserves workspace validation and packaging scripts. Paths and toolchain/cache configuration must be adapted when restoring to another machine.
- `evidence/` preserves intermediate passes and failures. These are historical development results, not proof that the current source passed every release gate.
- `SOURCE_FILES_SHA256.json` hashes every saved source file.

Private user captures, signing keys, caches and stale APK/binaries are excluded. Irreversible mailbox purge and uninstall remain CLI-only in the requested future admin interface.

## Historical snapshot

This directory records development before final qualification. Its status, handoff and source-file manifest describe checkpoint commit `fdce4298ac3c444ed2c224966d46d40cd196f700`. The sibling `../source/` now contains the final qualified source; use its `docs/rev5/evidence/rev5_validation.json` and the release manifest for final results. Historical failures above are retained and are not waived or counted as final passes.
