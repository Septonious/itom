# ITOM 1.0.0

The user-selected final release restores the RC2 transport. No post-RC2 transport or quota/journal/wallet policy is applied. Release identity strings and Android packaging/signing safeguards are updated. Android application ID remains org.itom.client; versionCode 10014.

Historical RC2 used org.itom.client.dev.rev7. This build keeps the current package identity instead. A matching signer is still required to update an installed org.itom.client app. Preserve the app/profile, wallets, server state and journals; never uninstall/clear data merely to bypass a signer error. Rollback across later retained-state formats is not automatically certified.

The original qualification thresholds are unchanged. Selecting RC2 as final is the user's release decision based on field experience; it is not a claim that all historical synthetic or live-provider gates passed. Transport rework and idle/light RTT experiments belong to 1.1.0.
