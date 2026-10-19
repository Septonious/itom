# Signing setup status

The user authorized generation of a new private signing key if the existing key could not be found. A new RSA3072 key was generated in the private execution workspace, protected by random store/key passwords and mode600 files. Only its PUBLIC certificate is committed here. It has NOT been provisioned into GitHub Actions secrets; no private key or password is committed or uploaded as an artifact.

The GitHub connection supports repository content but not Actions secrets; local gh authentication reports an invalid credential. The workflow supports four conventional signing secret names listed in source/docs/STATE_AND_SIGNING.md. It uses them if configured, rejects partial configuration, and explicitly records temporary-debug fallback when none are configured. Compare the actual APK public certificate in the build artifact SIGNER.txt; do not assume it matches this unprovisioned certificate or the installed RC12 APK.

The selected transport correction is server-side. Existing RC12 users should keep their app/profile and update the server, avoiding another wallet reset. New signing-key provisioning/migration remains outstanding; generated local private material must be transferred through a private channel, never a public repository/artifact or chat paste.
