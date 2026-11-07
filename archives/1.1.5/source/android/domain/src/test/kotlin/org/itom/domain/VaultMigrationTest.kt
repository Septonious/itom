package org.itom.domain

import kotlinx.serialization.json.*
import org.junit.Test
import java.util.Base64
import kotlin.test.*

class VaultMigrationTest {
    private val psk = Base64.getEncoder().encodeToString(ByteArray(32) { 7 })
    // Frozen old fixture, independent from the migration's private fingerprint.
    private fun oldProfile() = ServerProfile(profileId = "demo", name = "Демонстрационный сервер",
        serverId = "00000000000000000000000000000001", userId = "88888888", providerId = "demo",
        encryptionKeyRef = "keystore:demo_pair", mailboxNamespace = "ITOM/88888888/demo",
        accounts = (1..10).map { MailAccount("demo_$it", "Линии $it", "imap.example.invalid",
            credentialRef = "keystore:demo_$it", providerId = "demo") })
    private fun oldVault(p: ServerProfile = oldProfile()) = VaultDocument(profiles = listOf(p), selectedProfileId = p.profileId,
        revision = 9, incompleteProfileIds = setOf(p.profileId))
    private fun legacy(v: VaultDocument, ids: Set<String> = v.incompleteProfileIds): String {
        val root = Codec.json.encodeToJsonElement(v).jsonObject
        return JsonObject((root - "incompleteProfileIds") + ("version" to JsonPrimitive(1)) +
            ("demoProfileIds" to Codec.json.encodeToJsonElement(ids)) + ("largeText" to JsonPrimitive(true))).toString()
    }
    private fun error(code: String, block: () -> Unit) { assertEquals(code, assertFailsWith<DomainError>(block = block).code) }

    @Test fun untouchedBuiltInIsRemovedAndMigrationRunsOnce() {
        val result = Codec.decodeVault(legacy(oldVault()))
        assertTrue(result.migrated)
        val v = result.document
        assertEquals(2, v.version); assertEquals(10L, v.revision)
        assertTrue(v.profiles.isEmpty()); assertNull(v.selectedProfileId); assertTrue(v.incompleteProfileIds.isEmpty())
        val encoded = Codec.encode(v)
        assertFalse(encoded.contains("demoProfileIds")); assertFalse(encoded.contains("largeText"))
        val reloaded = Codec.decodeVault(encoded)
        assertFalse(reloaded.migrated); assertEquals(encoded, Codec.encode(reloaded.document))
    }
    @Test fun editedMetadataIsPreservedAsIncomplete() {
        val p = oldProfile()
        val variants = listOf(p.copy(name = "My server"), p.copy(userId = "87654321"),
            p.copy(mailboxNamespace = "ITOM/88888888/mine"), p.copy(providerId = "mine", accounts = p.accounts.map { it.copy(providerId = "mine") }),
            p.copy(accounts = p.accounts.mapIndexed { index, a -> if (index == 0) a.copy(host = "imap.mine.invalid") else a }))
        variants.forEach { edited ->
            val v = Codec.vault(legacy(oldVault(edited)))
            assertEquals(listOf(edited), v.profiles); assertEquals(setOf(edited.profileId), v.incompleteProfileIds)
            assertEquals(edited.profileId, v.selectedProfileId)
        }
    }
    @Test fun evenOneStoredCredentialPreventsRemoval() {
        val p = oldProfile(); val ref = p.accounts.first().credentialRef
        val source = oldVault().copy(credentials = mapOf(ref to MailCredential("kept@example.invalid", "KEEP_PASSWORD")))
        val v = Codec.vault(legacy(source))
        assertEquals(listOf(p), v.profiles); assertEquals(setOf("demo"), v.incompleteProfileIds)
        assertEquals("KEEP_PASSWORD", v.credentials.getValue(ref).password)
        assertEquals("kept@example.invalid", ProfileEditing.open(v, "demo").initial.accounts.first().username)
    }
    @Test fun storedKeyWithoutCredentialsIsPreserved() {
        val p = oldProfile(); val source = oldVault().copy(keys = mapOf(p.encryptionKeyRef to PairSecret(p.serverId, psk)))
        val v = Codec.vault(legacy(source))
        assertEquals(listOf(p), v.profiles); assertEquals(psk, v.keys.getValue(p.encryptionKeyRef).pskBase64)
        assertEquals(setOf("demo"), v.incompleteProfileIds)
    }
    @Test fun fullyConfiguredFormerDemoBecomesRegularProfile() {
        val p = oldProfile(); val source = oldVault().copy(
            credentials = p.accounts.associate { it.credentialRef to MailCredential("user-${it.id}", "PASSWORD-${it.id}") },
            keys = mapOf(p.encryptionKeyRef to PairSecret(p.serverId, psk)))
        val v = Codec.vault(legacy(source))
        assertEquals(listOf(p), v.profiles); assertTrue(v.incompleteProfileIds.isEmpty())
        assertEquals(psk, v.keys.getValue(p.encryptionKeyRef).pskBase64)
        p.accounts.forEach { assertEquals("PASSWORD-${it.id}", v.credentials.getValue(it.credentialRef).password) }
    }
    @Test fun removalPreservesOtherProfilesRulesAndSelection() {
        val other = oldProfile().copy(profileId = "mine", serverId = "11111111111111111111111111111111", name = "Mine",
            encryptionKeyRef = "keystore:mine_key", accounts = listOf(oldProfile().accounts.first().copy(credentialRef = "keystore:mine_mail")))
        val settings = AppSettings(routing = RoutingPolicy(rules = AddressRules(Block = listOf("example.org"))),
            appRouting = AppPolicy(AppMode.EXCLUDE_SELECTED, listOf("org.example.app")))
        val source = oldVault().copy(profiles = listOf(oldProfile(), other), settings = settings,
            credentials = mapOf("keystore:mine_mail" to MailCredential("my-user", "MY_PASSWORD")),
            keys = mapOf("keystore:mine_key" to PairSecret(other.serverId, psk)))
        val v = Codec.vault(legacy(source))
        assertEquals(listOf(other), v.profiles); assertEquals("mine", v.selectedProfileId)
        assertEquals(settings, v.settings); assertEquals("MY_PASSWORD", v.credentials.values.single().password)
        assertTrue(v.incompleteProfileIds.isEmpty())
        assertNull(Codec.vault(legacy(source.copy(selectedProfileId = null))).selectedProfileId)
        assertEquals("mine", Codec.vault(legacy(source.copy(selectedProfileId = "mine"))).selectedProfileId)
    }
    @Test fun matchingNameWithoutLegacyMarkerIsNeverDeleted() {
        val p = oldProfile(); val source = oldVault().copy(incompleteProfileIds = emptySet(),
            credentials = p.accounts.associate { it.credentialRef to MailCredential("user", "pass") },
            keys = mapOf(p.encryptionKeyRef to PairSecret(p.serverId, psk)))
        val v = Codec.vault(legacy(source))
        assertEquals(source.profiles, v.profiles); assertTrue(v.incompleteProfileIds.isEmpty())
    }
    @Test fun malformedLegacyVaultIsRejectedWithoutDiscardingAnything() {
        error("VAULT_CREDENTIALS") { Codec.vault(legacy(oldVault().copy(credentials = mapOf("keystore:orphan" to MailCredential("user", "pass"))))) }
        error("CREDENTIAL_FORMAT") { Codec.vault(legacy(oldVault().copy(credentials = mapOf("keystore:demo_1" to MailCredential("user", ""))))) }
        error("INCOMPLETE_PROFILE_MISSING") { Codec.vault(legacy(oldVault(), setOf("missing"))) }
        error("VAULT_CREDENTIALS") { Codec.vault(legacy(oldVault(), emptySet())) }
        error("REVISION_LIMIT") { Codec.vault(legacy(oldVault().copy(revision = Long.MAX_VALUE))) }
    }
    @Test fun currentVaultCannotReintroduceLegacyFlagsOrUnknownVersions() {
        val root = Codec.json.encodeToJsonElement(VaultDocument()).jsonObject
        for (field in listOf("demoProfileIds", "largeText")) {
            error("VAULT_FORMAT") { Codec.vault(JsonObject(root + (field to JsonPrimitive(false))).toString()) }
        }
        error("VAULT_VERSION") { Codec.vault(JsonObject(root + ("version" to JsonPrimitive(99))).toString()) }
        assertFalse(Codec.decodeVault(Codec.encode(VaultDocument())).migrated)
    }
    @Test fun rejectedConnectionHasNoSimulatedSessionOrMetrics() {
        val machine = SessionMachine(); val old = machine.begin("previous", 1, 4)
        machine.connected(old, 1, interfaceUp = true)
        machine.metrics(old, Metrics(10_000, 20_000, 10, 2, 4, 4))
        machine.reject("current", 2, 20, "CORE_NOT_INTEGRATED")
        val s = machine.state.value
        assertEquals(Phase.ERROR, s.phase); assertEquals("CORE_NOT_INTEGRATED", s.error)
        assertEquals(Metrics(totalLines = 20), s.metrics); assertNull(s.startedAt)
        assertTrue(s.history.isEmpty()); assertFalse(s.vpnInterfaceUp); assertEquals("current", s.profileId)
        assertFalse(machine.connected(old, 2)); assertFalse(machine.fail(old, "LATE"))
        assertFalse(machine.connected(s.generation, 2))
        machine.stop(); assertEquals(Phase.OFF, machine.state.value.phase)
    }
}
