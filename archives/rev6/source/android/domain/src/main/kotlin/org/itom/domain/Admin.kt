package org.itom.domain

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.*

@Serializable data class AdminView(val authorized: Boolean = false, val status: AdminStatus? = null, val pending: AdminPending? = null, val error: String? = null)
@Serializable data class AdminStatus(val revision: String = "", val admin: Boolean = false, val current_uid: String = "", val provider: String = "", val service: String = "", val diagnostics: String = "on", val global_message: String = "", val users: List<AdminUser> = emptyList(), val accounts: List<AdminAccount> = emptyList(), val transport: AdminResources = AdminResources(), val traffic: JsonObject? = null, val watchdog_available: Boolean = false)
@Serializable data class AdminUser(val id: String, val name: String, val enabled: Boolean, val message: String = "")
@Serializable data class AdminAccount(val id: String, val host: String, val port: Int, val tls: String, val username: String)
@Serializable data class AdminResources(val layout: String = "auto", val data_shards: Int = 1, val account_sessions: Int = 10, val endpoint_connections: Int = 128, val login_parallelism: Int = 4)
@Serializable data class AdminPending(val id: String, val owner: String = "", val state: String, val loss_deadline_ms: Long = 0, val healthy_since_ms: Long = 0, val server_time_ms: Long = 0) {
    val active: Boolean get() = state in setOf("armed", "observing", "rolling_back")
}
@Serializable data class AdminReply(val ok: Boolean = false, val error: String? = null, val status: AdminStatus? = null, val pending: AdminPending? = null, val text: String = "", val data: String = "", val id: String = "", val total: Long = 0)

object AdminCodec {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }
    fun view(raw: String): AdminView = runCatching {
        Codec.preflight(raw, 1 shl 20)
        json.decodeFromString<AdminView>(raw).let { view ->
            val uid = view.status?.current_uid.orEmpty()
            view.copy(authorized = view.authorized && view.status?.admin == true && uid.length == 8 && uid[0] != '0' && uid.all { it in '0'..'9' })
        }
    }.getOrDefault(AdminView())
    fun reply(raw: String): AdminReply {
        Codec.preflight(raw, 1 shl 20)
        return json.decodeFromString<AdminReply>(raw)
    }
    fun trialSeconds(pending: AdminPending, elapsedSinceSnapshotMs: Long): Long =
        ((pending.loss_deadline_ms - pending.server_time_ms - elapsedSinceSnapshotMs).coerceAtLeast(0) + 999) / 1000
    fun messageValid(text: String): Boolean = text.toByteArray(Charsets.UTF_8).size <= 2048 && text.none { it == '\u0000' || it == '\u001b' || it == '\r' }
}
