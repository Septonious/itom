# ITOM 1.0.0 RC10.1 diagnostic build

Build source: `76d08dc80a23f9322350ccdc570bad86b5768df6`.
Workflow: https://github.com/Septonious/itom/actions/runs/39255057487

The Linux amd64/arm64 binaries and source archive are verified. Android APK compilation is in progress; its final file, checksum and build evidence will be added here when complete.

This is the RC10 transport with observation-only stage profiling. Existing incomplete performance qualification remains incomplete. Use the matched client/server pair for field diagnostics. Keep configuration, identities, quota journals and wallets.

On Linux, make the selected architecture's downloaded binary executable (`chmod +x FILE.bin`) and use the existing manager upgrade procedure. `FILE.bin version` must report `itom-1-0-0-rc10-1`.
