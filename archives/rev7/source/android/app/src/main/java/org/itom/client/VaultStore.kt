package org.itom.client

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.AtomicFile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.itom.domain.*
import java.io.File
import java.nio.ByteBuffer
import java.security.KeyStore
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** One encrypted AtomicFile includes metadata and secrets: no two-store commit window. */
class VaultStore(private val context: Context) {
    private val file = AtomicFile(File(context.noBackupFilesDir, "itom.vault"))
    private val lock = Mutex()
    private var document: VaultDocument? = null
    private val mutable = MutableStateFlow(PublicConfig())
    val state = mutable.asStateFlow()
    private val aad = "${context.packageName}:itom-vault:1".toByteArray()
    private val alias = "itom.vault.v1"
    private fun key(create: Boolean): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        demand(create, "VAULT_KEY_UNAVAILABLE")
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true).build())
        }.generateKey()
    }
    suspend fun load() = withContext(Dispatchers.IO) { lock.withLock {
        if (document != null) return@withLock
        try {
            val exists = file.baseFile.exists() || File(file.baseFile.path + ".bak").exists()
            val decoded = if (!exists) DecodedVault(VaultDocument(), migrated = false) else {
                val encrypted = file.openRead().use { input ->
                    val size = input.channel.size()
                    demand(size in 33..(Codec.VAULT_LIMIT + 1024).toLong(), "VAULT_SIZE")
                    input.readBytes()
                }
                demand(encrypted.take(4).toByteArray().contentEquals(byteArrayOf(73, 84, 86, 1)), "VAULT_HEADER")
                val cipher = Cipher.getInstance("AES/GCM/NoPadding")
                cipher.init(Cipher.DECRYPT_MODE, key(false), GCMParameterSpec(128, encrypted.copyOfRange(4, 16)))
                cipher.updateAAD(aad)
                val plain = cipher.doFinal(encrypted, 16, encrypted.size - 16)
                try { Codec.decodeVault(Charsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(plain)).toString()) } finally { plain.fill(0) }
            }
            val v = decoded.document
            // Persist migration before exposing its result. The encrypted envelope/Keystore alias stay unchanged.
            if (decoded.migrated) save(v)
            document = v; publish(v)
        } catch (e: CancellationException) { throw e }
        catch (_: Exception) {
            // Never replace unreadable data with an empty vault; recovery is an explicit future flow.
            mutable.value = PublicConfig(loading = false, error = "VAULT_UNAVAILABLE")
        }
    } }
    /** Captured atomically with the selected revision; secrets never enter an Intent. */
    suspend fun runtime(profileId: String, revision: Long, dns: List<String>): RuntimeConfig = withContext(Dispatchers.IO) { lock.withLock {
        val v = document ?: throw DomainError("VAULT_UNAVAILABLE")
        demand(v.revision == revision, "CONFIG_CHANGED")
        val p = v.profiles.singleOrNull { it.profileId == profileId } ?: throw DomainError("PROFILE_MISSING")
        demand(profileId !in v.incompleteProfileIds, "PROFILE_INCOMPLETE")
        Validation.vault(v)
        val key = java.util.Base64.getDecoder().decode(v.keys.getValue(p.encryptionKeyRef).pskBase64)
        val user = org.json.JSONObject().put("uid", p.userId).put("name", p.name).put("enabled", true)
            .put("key", key.joinToString("") { "%02x".format(it.toInt() and 255) })
            .put("up", p.mailboxNamespace + "-up").put("down", p.mailboxNamespace + "-down")
        key.fill(0)
        val accounts = org.json.JSONArray()
        p.accounts.filter { it.enabled }.forEach { account ->
            val secret = v.credentials.getValue(account.credentialRef)
            accounts.put(org.json.JSONObject().put("id", account.id).put("imap", org.json.JSONObject()
                .put("host", account.host).put("port", account.port).put("tls", if (account.tls == "imaps") "tls" else "starttls")
                .put("username", secret.username).put("password", secret.password)))
        }
        val server = org.json.JSONArray(); p.serverId.chunked(2).forEach { server.put(it.toInt(16)) }
        val profile = org.json.JSONObject().put("schema", 1).put("kind", "itom-android")
            .put("server", server).put("provider", p.providerId).put("user", user).put("accounts", accounts)
        val resources = p.transportResources
        profile.put("transport", org.json.JSONObject().put("layout", resources.layout)
            .put("data_shards", resources.dataShards).put("account_sessions", resources.accountSessions)
            .put("endpoint_connections", resources.endpointConnections).put("login_parallelism", resources.loginParallelism)
            .put("participants", resources.participants))
        val socks = org.json.JSONObject().put("Port", v.settings.socks.port).put("Authentication", v.settings.socks.authentication)
            .put("Username", v.socksCredential?.username ?: "").put("Password", v.socksCredential?.password ?: "")
        RuntimeConfig(p, v.settings, org.json.JSONObject().put("Profile", profile).put("DNS", org.json.JSONArray(dns)).put("Socks", socks)
            .put("Diagnostics", v.settings.diagnosticsEnabled).toString())
    } }
    private fun publish(v: VaultDocument) {
        mutable.value = PublicConfig(profiles = v.profiles, selectedProfileId = v.selectedProfileId, settings = v.settings,
            revision = v.revision, incompleteProfileIds = v.incompleteProfileIds, loading = false)
    }
    private fun save(v: VaultDocument) {
        val plain = Codec.encode(v).toByteArray(Charsets.UTF_8)
        try {
            demand(plain.size <= Codec.VAULT_LIMIT, "VAULT_SIZE")
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, key(true)); cipher.updateAAD(aad)
            demand(cipher.iv.size == 12, "VAULT_IV")
            val encrypted = byteArrayOf(73, 84, 86, 1) + cipher.iv + cipher.doFinal(plain)
            val output = file.startWrite()
            // AtomicFile can log sync/rename errors without throwing; verify the committed bytes too.
            try { output.write(encrypted); output.fd.sync(); file.finishWrite(output) }
            catch (e: Exception) { file.failWrite(output); throw e }
            // Do not call failWrite after finishWrite: older Android implementations could delete the base file.
            file.openRead().use { input ->
                demand(input.channel.size() == encrypted.size.toLong() && input.readBytes().contentEquals(encrypted), "VAULT_WRITE_FAILED")
            }
        } finally { plain.fill(0) }
    }
    private suspend fun update(block: (VaultDocument) -> VaultDocument) = withContext(Dispatchers.IO) { lock.withLock {
        val old = document ?: throw DomainError("VAULT_UNAVAILABLE")
        val candidate = block(old)
        if (candidate == old) return@withLock
        demand(old.revision < Long.MAX_VALUE, "REVISION_LIMIT")
        val next = candidate.copy(revision = old.revision + 1)
        Validation.vault(next)
        try { save(next) } catch (_: Exception) {
            // Commit status may be uncertain. Block subsequent edits until a read from disk reconciles it.
            document = null
            mutable.value = PublicConfig(loading = false, error = "VAULT_WRITE_FAILED")
            throw DomainError("VAULT_WRITE_FAILED")
        }
        document = next; publish(next)
    } }
    suspend fun socksDraft() = withContext(Dispatchers.IO) { lock.withLock {
        val v = document ?: throw DomainError("VAULT_UNAVAILABLE")
        SocksDraft(v.settings.socks.port.toString(), v.settings.socks.authentication,
            v.socksCredential?.username ?: "", v.socksCredential?.password ?: "", v.revision)
    } }
    suspend fun socks(draft: SocksDraft) = update { v ->
        demand(v.revision == draft.revision, "SOCKS_EDIT_CONFLICT")
        val port = draft.port.toIntOrNull() ?: throw DomainError("SOCKS_PORT")
        demand(port in 1024..65535, "SOCKS_PORT")
        val secret = if ((draft.username.isEmpty() || draft.password.isEmpty()) && !draft.authentication) null
            else SocksCredential(draft.username, draft.password)
        v.copy(settings = v.settings.copy(socks = SocksSettings(port, draft.authentication)), socksCredential = secret)
    }
    suspend fun diagnostics(enabled: Boolean) = update { v -> v.copy(settings = v.settings.copy(diagnosticsEnabled = enabled)) }
    suspend fun networks(policy: NetworkPolicy) = update { v -> v.copy(settings = v.settings.copy(networks = policy)) }
    suspend fun select(id: String) = update { v -> demand(v.profiles.any { it.profileId == id }, "PROFILE_MISSING"); v.copy(selectedProfileId = id) }
    suspend fun editSession(id: String) = withContext(Dispatchers.IO) { lock.withLock {
        ProfileEditing.open(document ?: throw DomainError("VAULT_UNAVAILABLE"), id)
    } }
    suspend fun editProfile(id: String, expectedRevision: Long, draft: ProfileDraft) = update { v ->
        ProfileEditing.apply(v, id, expectedRevision, draft)
    }
    suspend fun remove(id: String) = update { v ->
        val p = v.profiles.find { it.profileId == id } ?: throw DomainError("PROFILE_MISSING")
        val remaining = v.profiles.filterNot { it.profileId == id }
        v.copy(profiles = remaining, selectedProfileId = if (v.selectedProfileId == id) remaining.firstOrNull()?.profileId else v.selectedProfileId,
            credentials = v.credentials - p.accounts.map { it.credentialRef }.toSet(), keys = v.keys - p.encryptionKeyRef, incompleteProfileIds = v.incompleteProfileIds - id)
    }
    suspend fun rules(action: RouteAction, entries: List<String>) = update { v ->
        val canonical = entries.filter { it.isNotBlank() }.map { CompiledRule.parse(it).source }.distinct()
        v.copy(settings = v.settings.copy(routing = v.settings.routing.copy(rules = v.settings.routing.rules.with(action, canonical))))
    }
    suspend fun apps(policy: AppPolicy, catalog: List<InstalledApp>) = update { v ->
        AppSelection.validate(policy, catalog); v.copy(settings = v.settings.copy(appRouting = policy))
    }
    suspend fun importInvitation(invitation: Invitation) = update { v ->
        Validation.invitation(invitation, System.currentTimeMillis() / 1000)
        demand(v.profiles.none { it.serverId == invitation.profile.serverId }, "DUPLICATE_SERVER")
        val id = UUID.randomUUID().toString().replace("-", "")
        val keyRef = "keystore:pair_$id"
        val secrets = mutableMapOf<String, MailCredential>()
        val accounts = invitation.profile.accounts.mapIndexed { index, a ->
            val ref = "keystore:mail_${id}_$index"
            secrets[ref] = invitation.credentials.getValue(a.credentialRef)
            a.copy(credentialRef = ref)
        }
        val profile = invitation.profile.copy(profileId = id, encryptionKeyRef = keyRef, accounts = accounts)
        v.copy(profiles = v.profiles + profile, selectedProfileId = id, credentials = v.credentials + secrets,
            keys = v.keys + (keyRef to PairSecret(invitation.pairId, invitation.pskBase64)))
    }
}

class RuntimeConfig(val profile: ServerProfile, val settings: AppSettings, val json: String) { override fun toString() = "RuntimeConfig(<redacted>)" }
