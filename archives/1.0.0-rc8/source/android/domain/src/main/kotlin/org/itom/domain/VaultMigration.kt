package org.itom.domain

import kotlinx.serialization.json.*

class DecodedVault(val document: VaultDocument, val migrated: Boolean) {
    override fun toString() = "DecodedVault(<redacted>)"
}

/** Read-only compatibility with v1; new vaults never contain demo state or large-text flags. */
internal object VaultMigration {
    fun fromV1(root: JsonObject): VaultDocument {
        demand("incompleteProfileIds" !in root, "VAULT_FORMAT")
        val legacyIds = root["demoProfileIds"]?.let { Codec.json.decodeFromJsonElement<Set<String>>(it) } ?: emptySet()
        root["largeText"]?.let { Codec.json.decodeFromJsonElement<Boolean>(it) }
        val adapted = JsonObject((root - setOf("demoProfileIds", "largeText")) +
            ("version" to JsonPrimitive(2)) + ("incompleteProfileIds" to Codec.json.encodeToJsonElement(legacyIds)))
        val old = Codec.json.decodeFromJsonElement<VaultDocument>(Codec.upgradeVaultSettings(adapted))
        // Validate before removing anything: malformed or orphaned secrets cannot be silently discarded.
        Validation.vault(old)
        val removed = old.profiles.filter { p ->
            p.profileId in legacyIds && pristine(p) && p.encryptionKeyRef !in old.keys &&
                p.accounts.none { it.credentialRef in old.credentials }
        }.map { it.profileId }.toSet()
        val profiles = old.profiles.filterNot { it.profileId in removed }
        val incomplete = profiles.filter { p -> p.profileId in legacyIds &&
            (p.encryptionKeyRef !in old.keys || p.accounts.any { it.credentialRef !in old.credentials })
        }.map { it.profileId }.toSet()
        demand(old.revision < Long.MAX_VALUE, "REVISION_LIMIT")
        return old.copy(profiles = profiles, incompleteProfileIds = incomplete, revision = old.revision + 1,
            selectedProfileId = if (old.selectedProfileId in removed) profiles.firstOrNull()?.profileId else old.selectedProfileId)
            .also(Validation::vault)
    }

    // Exact historical built-in fingerprint, never a name/domain heuristic for user profiles.
    private fun pristine(p: ServerProfile): Boolean = p == ServerProfile(
        profileId = "demo", name = "Демонстрационный сервер", serverId = "00000000000000000000000000000001",
        userId = "88888888", providerId = "demo", encryptionKeyRef = "keystore:demo_pair",
        mailboxNamespace = "ITOM/88888888/demo",
        accounts = (1..10).map { MailAccount("demo_$it", "Линии $it", "imap.example.invalid",
            credentialRef = "keystore:demo_$it", providerId = "demo") })
}
