# Signing setup status

The user authorized generation of a new private signing key if the existing key could not be found. A new RSA3072 key was generated in the private execution workspace, protected by random store/key passwords and mode600 files. Only its PUBLIC certificate is committed here. It has NOT been provisioned into GitHub Actions secrets; no private key or password is committed or uploaded as an artifact.

The GitHub connection supports repository content but not Actions secrets; local gh authentication reports an invalid credential. The workflow supports four conventional signing secret names listed in source/docs/STATE_AND_SIGNING.md. It uses them if configured, rejects partial configuration, and explicitly records temporary-debug fallback when none are configured. Compare the actual APK public certificate in the build artifact SIGNER.txt; do not assume it matches this unprovisioned certificate or the installed RC12 APK.

The selected transport correction is server-side. Existing RC12 users should keep their app/profile and update the server, avoiding another wallet reset. New signing-key provisioning/migration remains outstanding; generated local private material must be transferred through a private channel, never a public repository/artifact or chat paste.

## One-command private provisioning

On your own computer with JDK/keytool and an authenticated GitHub CLI with repository-secrets write access, run from the repository root:

```sh
python3 archives/1.0.0-rc13/audit/setup_android_signing.py --private-dir ../itom-private-signing
```

The helper generates a key locally if no private settings exist, saves a private backup outside Git, and sets the four Actions secrets through stdin. It refuses to overwrite existing signing secrets or an unmatched existing keystore. If using the key already generated in this execution workspace, point --private-dir at its private-signing directory through your private workspace access; do not paste or commit that directory. This connected session cannot perform the secret write with its current credentials. No additional runtime transport change or APK rebuild is needed to test the RC13 server with installed RC12 Android.
