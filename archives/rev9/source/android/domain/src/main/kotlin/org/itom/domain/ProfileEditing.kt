package org.itom.domain

import java.util.Locale
import java.util.UUID

/** In-memory editor values only: never serialize drafts into saved instance state or logs. */
data class MailAccountDraft(
    val localRef: String, val id: String, val name: String, val host: String, val port: String,
    val enabled: Boolean, val username: String, val password: String, val dialIP: String = "",
) { override fun toString() = "MailAccountDraft(<redacted>)" }

data class ProfileDraft(
    val name: String, val serverId: String, val userId: String, val providerId: String,
    val mailboxNamespace: String, val pairId: String, val pskBase64: String,
    val accounts: List<MailAccountDraft>,
) { override fun toString() = "ProfileDraft(<redacted>)" }

class ProfileEditSession(val profileId: String, val revision: Long, val initial: ProfileDraft) {
    override fun toString() = "ProfileEditSession(<redacted>)"
}

object ProfileEditing {
    fun open(vault: VaultDocument, profileId: String): ProfileEditSession {
        val p = vault.profiles.find { it.profileId == profileId } ?: throw DomainError("PROFILE_MISSING")
        val key = vault.keys[p.encryptionKeyRef]
        val draft = ProfileDraft(p.name, p.serverId, p.userId, p.providerId, p.mailboxNamespace,
            key?.pairId.orEmpty(), key?.pskBase64.orEmpty(), p.accounts.map { account ->
                val credential = vault.credentials[account.credentialRef]
                MailAccountDraft(account.credentialRef, account.id, account.name, account.host, account.port.toString(),
                    account.enabled, credential?.username.orEmpty(), credential?.password.orEmpty(), account.dialIP)
            })
        return ProfileEditSession(profileId, vault.revision, draft)
    }

    fun newAccount(existing: List<MailAccountDraft>): MailAccountDraft {
        val token = UUID.randomUUID().toString().replace("-", "")
        return MailAccountDraft("keystore:mail_$token", "mail_${token.take(12)}", "Account ${existing.size + 1}",
            existing.firstOrNull()?.host.orEmpty(), existing.firstOrNull()?.port ?: "993", true, "", "", existing.firstOrNull()?.dialIP.orEmpty())
    }

    /** Produces one complete replacement. VaultStore validates/encrypts/commits it under its mutex. */
    fun apply(vault: VaultDocument, profileId: String, expectedRevision: Long, draft: ProfileDraft): VaultDocument {
        val old = vault.profiles.find { it.profileId == profileId } ?: throw DomainError("PROFILE_MISSING")
        demand(vault.revision == expectedRevision, "PROFILE_EDIT_CONFLICT")
        val provider = draft.providerId.trim().lowercase(Locale.ROOT)
        val accounts = draft.accounts.map { a ->
            val port = a.port.trim().toIntOrNull() ?: throw DomainError("IMAP_PORT")
            demand(port in 1..65535, "IMAP_PORT")
            MailAccount(a.id.trim(), a.name.trim(), a.host.trim(), port, credentialRef = a.localRef,
                enabled = a.enabled, providerId = provider, dialIP = a.dialIP.trim(),
                tls = old.accounts.find { it.id == a.id }?.tls ?: "imaps")
        }
        val profile = old.copy(name = draft.name.trim(), serverId = draft.serverId.trim().lowercase(Locale.ROOT),
            userId = draft.userId.trim(), providerId = provider, mailboxNamespace = draft.mailboxNamespace.trim(), accounts = accounts)
        Validation.profile(profile)
        val credentials = draft.accounts.map { a ->
            a.localRef to MailCredential(a.username, a.password) // Passwords must not be trimmed.
        }.toMap()
        val pairId = draft.pairId.trim().lowercase(Locale.ROOT)
        val psk = draft.pskBase64.trim()
        val key = PairSecret(pairId, psk)
        val next = vault.copy(
            profiles = vault.profiles.map { if (it.profileId == profileId) profile else it },
            credentials = (vault.credentials - old.accounts.map { it.credentialRef }.toSet()) + credentials,
            keys = vault.keys + (old.encryptionKeyRef to key),
            incompleteProfileIds = vault.incompleteProfileIds - profileId,
        )
        // Checks collisions against other profiles and all secret references before anything is written.
        Validation.vault(next)
        return next
    }
}
