# Rev5 unstable development checkpoint

This is a recovery snapshot requested during session network trouble. No Rev5 release is authorized by this snapshot: full current-source qualification is incomplete.

- `../source/` contains the exact editable source, tests and build tools.
- `STATUS.json` records implemented work, pending work and acceptance limits.
- `CURRENT_HANDOFF.md` contains the detailed resume instructions and historical diagnoses. Its newest block overrides older status statements.
- `drivers/` preserves workspace validation and packaging scripts. Paths and toolchain/cache configuration must be adapted when restoring to another machine.
- `evidence/` preserves intermediate passes and failures. These are historical development results, not proof that the current source passed every release gate.
- `SOURCE_FILES_SHA256.json` hashes every saved source file.

Private user captures, signing keys, caches and stale APK/binaries are excluded. Irreversible mailbox purge and uninstall remain CLI-only in the requested future admin interface.
