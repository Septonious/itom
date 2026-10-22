package org.itom.domain

import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.*
import java.util.Base64
import java.nio.ByteBuffer

object Codec {
    val json = Json { ignoreUnknownKeys = false; encodeDefaults = true; isLenient = false; coerceInputValues = false; prettyPrint = true }
    const val INVITATION_LIMIT = 128 * 1024
    const val VAULT_LIMIT = 8 * 1024 * 1024

    data class ProfileLint(val code: String, val line: Int = 0, val column: Int = 0)

    /** Reject duplicate keys, excessive nesting and invalid JSON before decoding. */
    fun preflight(text: String, limit: Int, onSyntaxError: ((String, Int) -> Unit)? = null) {
        demand(text.toByteArray(Charsets.UTF_8).size <= limit, "DOCUMENT_TOO_LARGE")
        var pos = 0
        fun ws() { while (pos < text.length && text[pos] in " \n\r\t") pos++ }
        fun char(c: Char) { ws(); demand(pos < text.length && text[pos] == c, "INVALID_JSON"); pos++ }
        fun string(): String {
            ws(); val start = pos; char('"')
            while (pos < text.length) {
                val c = text[pos]
                demand(c.code >= 0x20, "INVALID_JSON")
                pos++
                if (c == '"') return json.decodeFromString(text.substring(start, pos))
                if (c == '\\') {
                    demand(pos < text.length, "INVALID_JSON")
                    val escape = text[pos]
                    demand(escape in "\"\\/bfnrtu", "INVALID_JSON"); pos++
                    if (escape == 'u') repeat(4) {
                        demand(pos < text.length && text[pos] in "0123456789abcdefABCDEF", "INVALID_JSON"); pos++
                    }
                }
            }
            throw DomainError("INVALID_JSON")
        }
        fun value(depth: Int) {
            demand(depth <= 32, "JSON_TOO_DEEP"); ws(); demand(pos < text.length, "INVALID_JSON")
            when (text[pos]) {
                '{' -> {
                    pos++; ws(); val keys = hashSetOf<String>()
                    if (pos < text.length && text[pos] == '}') { pos++; return }
                    while (true) {
                        val key = string(); demand(keys.add(key), "DUPLICATE_JSON_KEY")
                        char(':'); value(depth + 1); ws(); demand(pos < text.length, "INVALID_JSON")
                        if (text[pos] == '}') { pos++; break }; char(',')
                    }
                }
                '[' -> {
                    pos++; ws(); if (pos < text.length && text[pos] == ']') { pos++; return }
                    while (true) { value(depth + 1); ws(); demand(pos < text.length, "INVALID_JSON"); if (text[pos] == ']') { pos++; break }; char(',') }
                }
                '"' -> { string() }
                else -> {
                    val start = pos
                    while (pos < text.length && text[pos] !in ",]} \n\r\t") pos++
                    val token = text.substring(start, pos)
                    if (token !in setOf("true", "false", "null") && !token.matches(Regex("-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?"))) {
                        pos = start; throw DomainError("INVALID_JSON")
                    }
                }
            }
        }
        try {
            value(0); ws(); demand(pos == text.length, "INVALID_JSON")
            json.parseToJsonElement(text)
        } catch (e: DomainError) { onSyntaxError?.invoke(e.code, pos); throw e }
        catch (_: Exception) { onSyntaxError?.invoke("INVALID_JSON", pos); throw DomainError("INVALID_JSON") }
    }
    fun lintInvitation(text: String, now: Long): ProfileLint? {
        var issue: ProfileLint? = null
        return try {
            val raw = invitationText(text)
            preflight(raw, INVITATION_LIMIT) { code, index ->
                val prefix = raw.take(index.coerceIn(0, raw.length))
                issue = ProfileLint(code, prefix.count { it == '\n' } + 1, prefix.length - prefix.lastIndexOf('\n'))
            }
            invitation(raw, now)
            null
        } catch (e: DomainError) { issue ?: ProfileLint(e.code) }
    }
    const val INVITATION_LINK_PREFIX = "itom://import#"
    fun invitationText(input: String): String {
        val text = input.trim()
        if (!text.startsWith(INVITATION_LINK_PREFIX)) return input
        demand(text.length <= INVITATION_LIMIT * 4 / 3 + 32, "INPUT_LIMIT")
        val encoded = text.removePrefix(INVITATION_LINK_PREFIX)
        demand(encoded.isNotEmpty() && encoded.all { it.isLetterOrDigit() && it.code < 128 || it == '-' || it == '_' }, "INVITATION_FORMAT")
        val bytes = try { Base64.getUrlDecoder().decode(encoded) } catch (_: IllegalArgumentException) { throw DomainError("INVITATION_FORMAT") }
        try {
            demand(bytes.size <= INVITATION_LIMIT, "INPUT_LIMIT")
            demand(Base64.getUrlEncoder().withoutPadding().encodeToString(bytes) == encoded, "INVITATION_FORMAT")
            return try { Charsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(bytes)).toString() }
            catch (_: Exception) { throw DomainError("INVITATION_FORMAT") }
        } finally { bytes.fill(0) }
    }
    fun formatInvitation(text: String): String {
        val raw = invitationText(text)
        preflight(raw, INVITATION_LIMIT)
        return json.encodeToString(JsonElement.serializer(), json.parseToJsonElement(raw))
    }
    fun invitation(text: String, now: Long): Invitation {
        val raw = invitationText(text)
        preflight(raw, INVITATION_LIMIT)
        val result = try { json.decodeFromString<Invitation>(raw) } catch (_: Exception) { throw DomainError("INVITATION_FORMAT") }
        Validation.invitation(result, now); return result
    }
    fun decodeVault(text: String): DecodedVault {
        preflight(text, VAULT_LIMIT)
        return try {
            val root = json.parseToJsonElement(text).jsonObject
            when (root["version"]?.jsonPrimitive?.int ?: 1) {
                1 -> DecodedVault(VaultMigration.fromV1(root), migrated = true)
                2 -> {
                    val adapted = upgradeVaultSettings(root)
                    DecodedVault(json.decodeFromJsonElement<VaultDocument>(adapted).also(Validation::vault), migrated = adapted != root)
                }
                else -> throw DomainError("VAULT_VERSION")
            }
        } catch (e: DomainError) { throw e } catch (_: Exception) { throw DomainError("VAULT_FORMAT") }
    }
    internal fun upgradeVaultSettings(root: JsonObject): JsonObject {
        val settings = root["settings"]?.jsonObject ?: return root
        var adapted = settings
        var version = adapted["schema_version"]?.jsonPrimitive?.int ?: 3
        if (version == 3) {
            adapted = JsonObject(adapted + ("schema_version" to JsonPrimitive(4)))
            version = 4
        }
        if (version == 4) {
            adapted = JsonObject(adapted + mapOf(
                "schema_version" to JsonPrimitive(5),
                "rtt_color_thresholds_ms" to json.encodeToJsonElement(RttThresholds()),
                "diagnostics_enabled" to JsonPrimitive(false),
            ))
        }
        return if (adapted == settings) root else JsonObject(root + ("settings" to adapted))
    }
    fun vault(text: String): VaultDocument = decodeVault(text).document
    fun settings(text: String): AppSettings {
        preflight(text, 1024 * 1024)
        val result = try {
            val root = json.parseToJsonElement(text).jsonObject
            val version = root["schema_version"]?.jsonPrimitive?.int ?: throw DomainError("SETTINGS_VERSION")
            var migrated = if (version == 2) {
                demand("app_routing" !in root, "INVALID_V2_SETTINGS")
                JsonObject(root + ("schema_version" to JsonPrimitive(3)) + ("app_routing" to json.encodeToJsonElement(AppPolicy())))
            } else root
            if (migrated["schema_version"]?.jsonPrimitive?.int == 3) {
                migrated = JsonObject(migrated + ("schema_version" to JsonPrimitive(4)))
            }
            if (migrated["schema_version"]?.jsonPrimitive?.int == 4) {
                migrated = JsonObject(migrated + mapOf(
                    "schema_version" to JsonPrimitive(5),
                    "rtt_color_thresholds_ms" to json.encodeToJsonElement(RttThresholds()),
                    "diagnostics_enabled" to JsonPrimitive(false),
                ))
            }
            json.decodeFromJsonElement<AppSettings>(migrated)
        } catch (e: DomainError) { throw e } catch (_: Exception) { throw DomainError("SETTINGS_FORMAT") }
        Validation.settings(result); return result
    }
    fun encode(vault: VaultDocument): String { Validation.vault(vault); return json.encodeToString(vault) }
    /** Deliberately not a pairing bundle: secrets cannot be exported from this screen. */
    fun exportPublic(profile: ServerProfile): String = json.encodeToString(profile)
}
