# Preserve wallet state when updating

The user confirmed app reinstalls/data clearing between builds. Android transport journals and wallets are in noBackupFilesDir/transport/<profileId>. They do not survive uninstall/data clearing. Importing a server invitation creates a new random profileId. Server grants from missing client wallets remain charged; the server cannot invent a zero-spend proof. RC13 does not erase that debt.

## Current RC12 installation

The selected RC13 correction is server-side: recoveryQuotaLocked and the retained local-reserve branch of tryLocalGrant run on the server. The RC12 wire codec and client grant acceptance paths are unchanged and already support explicit reduced fast grants. Keep the installed RC12 Android app and existing profile, update the server to RC13, and reconnect normally. This avoids creating another missing wallet. A paired native supervisor regression exercises the same unchanged client acceptance logic with the actual reduced server grants.

The RC13 APK is supplied for fresh installations or updates with a compatible signing certificate. A new default debug signer cannot replace an app signed with a different debug key. If Android reports UPDATE_INCOMPATIBLE, preserve the installed app and its data; use the server update rather than uninstalling. Clearing mailbox/server journals or recreating account IDs is not an update procedure.

## Persistent signing for later builds

The RC13 GitHub workflow optionally loads these four repository Actions secrets:

- ITOM_ANDROID_KEYSTORE_B64: base64 encoding of a privately held signing keystore.
- ITOM_ANDROID_STORE_PASSWORD: its store password.
- ITOM_ANDROID_KEY_ALIAS: the signing alias.
- ITOM_ANDROID_KEY_PASSWORD: its key password.

Configure them through GitHub Settings → Secrets and variables → Actions. Keep the original keystore privately backed up. Do not send key/password values in chat or commit them. All four secrets must be present together; partial configuration fails closed. The workflow decodes the key only in the temporary runner directory and never uploads it. Gradle uses the key for debug field-test APKs without changing org.itom.client. The public signer certificate is recorded in SIGNER.txt.

With no signing secrets the existing temporary debug-signing behavior remains explicit, and in-place compatibility is not claimed. A newly configured stable key cannot retroactively match an older temporary certificate. Migrating an existing installation to a different key requires a separate state-preserving migration; it is not accomplished by reinstalling and cannot recover already deleted wallet history.
