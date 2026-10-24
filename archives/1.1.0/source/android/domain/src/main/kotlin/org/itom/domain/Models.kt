package org.itom.domain

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class MailAccount(
    val id: String, val name: String, val host: String, val port: Int = 993,
    val tls: String = "imaps", @SerialName("verify_certificate") val verifyCertificate: Boolean = true,
    @SerialName("credential_ref") val credentialRef: String, val enabled: Boolean = true,
    @SerialName("provider_id") val providerId: String,
    @SerialName("dial_ip") val dialIP: String = "",
)
@Serializable
data class TransportResources(
    val layout: String = "auto",
    @SerialName("data_shards") val dataShards: Int = 1,
    @SerialName("account_sessions") val accountSessions: Int = 10,
    @SerialName("endpoint_connections") val endpointConnections: Int = 128,
    @SerialName("login_parallelism") val loginParallelism: Int = 4,
    val participants: Int = 1,
)
@Serializable
data class ServerProfile(
    @SerialName("schema_version") val schemaVersion: Int = 2,
    @SerialName("profile_id") val profileId: String, val name: String,
    @SerialName("server_id") val serverId: String, @SerialName("user_id") val userId: String,
    @SerialName("provider_id") val providerId: String,
    @SerialName("encryption_key_ref") val encryptionKeyRef: String,
    @SerialName("mailbox_namespace") val mailboxNamespace: String,
    val accounts: List<MailAccount>, @SerialName("transport_profile") val transportProfile: String = "balanced-lab-v1",
    @SerialName("transport_resources") val transportResources: TransportResources = TransportResources(),
) { val totalLines: Int get() = accounts.count { it.enabled } * 2 }
@Serializable
enum class RouteAction { VPN, @SerialName("Block") BLOCK, @SerialName("Direct") DIRECT }
@Serializable
data class AddressRules(val VPN: List<String> = emptyList(), val Block: List<String> = emptyList(), val Direct: List<String> = emptyList()) {
    fun entries(action: RouteAction): List<String> = when (action) { RouteAction.VPN -> VPN; RouteAction.BLOCK -> Block; RouteAction.DIRECT -> Direct }
    fun with(action: RouteAction, values: List<String>) = when (action) { RouteAction.VPN -> copy(VPN = values); RouteAction.BLOCK -> copy(Block = values); RouteAction.DIRECT -> copy(Direct = values) }
}
@Serializable
data class RoutingPolicy(
    @SerialName("schema_version") val schemaVersion: Int = 1, val scope: String = "global",
    val priority: List<RouteAction> = listOf(RouteAction.VPN, RouteAction.BLOCK, RouteAction.DIRECT),
    @SerialName("default_action") val defaultAction: RouteAction = RouteAction.VPN, val rules: AddressRules = AddressRules(),
)
@Serializable
enum class AppMode { @SerialName("all") ALL, @SerialName("exclude_selected") EXCLUDE_SELECTED, @SerialName("include_selected") INCLUDE_SELECTED }
@Serializable
data class AppPolicy(val mode: AppMode = AppMode.ALL, val packages: List<String> = emptyList()) {
    fun captures(packageName: String): Boolean = when (mode) { AppMode.ALL -> true; AppMode.EXCLUDE_SELECTED -> packageName !in packages; AppMode.INCLUDE_SELECTED -> packageName in packages }
}
data class InstalledApp(val packageName: String, val label: String, val system: Boolean, val uid: Int)
@Serializable
data class TrafficPolicy(val tcp: Boolean = true, val dns: String = "configured", val udp: String = "blocked", val ipv6: String = "blocked", @SerialName("automatic_direct_fallback") val automaticDirectFallback: Boolean = false)
@Serializable
data class DNSSettings(
    val primary: String = "31.131.251.83",
    val backup: String = "185.22.234.229",
    @SerialName("itom_fallback") val itomFallback: Boolean = false,
)
@Serializable
data class RttThresholds(@SerialName("green_max") val greenMax: Int = 399, @SerialName("yellow_max") val yellowMax: Int = 799)
@Serializable
data class AppSettings(
    @SerialName("schema_version") val schemaVersion: Int = 5, val traffic: TrafficPolicy = TrafficPolicy(),
    val routing: RoutingPolicy = RoutingPolicy(), @SerialName("app_routing") val appRouting: AppPolicy = AppPolicy(),
    @SerialName("rtt_color_thresholds_ms") val rttThresholds: RttThresholds = RttThresholds(),
    @SerialName("diagnostics_enabled") val diagnosticsEnabled: Boolean = false,
    val socks: SocksSettings = SocksSettings(),
    @SerialName("allowed_networks") val networks: NetworkPolicy = NetworkPolicy(),
    @SerialName("dns_resolver") val dnsResolver: DNSSettings = DNSSettings(),
)
enum class NetworkKind { WIFI, MOBILE, OTHER }
@Serializable
data class NetworkPolicy(val wifi: Boolean = true, val mobile: Boolean = true) {
    fun allows(kind: NetworkKind?): Boolean = when {
        kind == null || wifi && mobile -> true
        kind == NetworkKind.WIFI -> wifi
        kind == NetworkKind.MOBILE -> mobile
        else -> false
    }
}
@Serializable
class MailCredential(val username: String, val password: String) { override fun toString() = "MailCredential(<redacted>)" }
@Serializable
class PairSecret(@SerialName("pair_id") val pairId: String, @SerialName("psk_base64") val pskBase64: String) { override fun toString() = "PairSecret(<redacted>)" }
@Serializable
data class SocksSettings(val port: Int = 1080, val authentication: Boolean = false)
@Serializable
class SocksCredential(val username: String, val password: String) { override fun toString() = "SocksCredential(<redacted>)" }
data class SocksDraft(val port: String, val authentication: Boolean, val username: String, val password: String, val revision: Long) {
    override fun toString() = "SocksDraft(<redacted>)"
}
@Serializable
data class VaultDocument(
    val version: Int = 2, val profiles: List<ServerProfile> = emptyList(), val selectedProfileId: String? = null,
    val settings: AppSettings = AppSettings(), val revision: Long = 0,
    val socksCredential: SocksCredential? = null,
    val credentials: Map<String, MailCredential> = emptyMap(), val keys: Map<String, PairSecret> = emptyMap(),
    // Preserved legacy profiles whose credentials/key still need to be filled in the editor.
    val incompleteProfileIds: Set<String> = emptySet(),
)
data class PublicConfig(
    val profiles: List<ServerProfile> = emptyList(), val selectedProfileId: String? = null,
    val settings: AppSettings = AppSettings(), val revision: Long = 0,
    val incompleteProfileIds: Set<String> = emptySet(), val loading: Boolean = true, val error: String? = null,
) { val selected: ServerProfile? get() = profiles.find { it.profileId == selectedProfileId } }
@Serializable
class Invitation(
    @SerialName("schema_version") val schemaVersion: Int, val profile: ServerProfile,
    @SerialName("pair_id") val pairId: String, @SerialName("psk_base64") val pskBase64: String,
    val credentials: Map<String, MailCredential>, @SerialName("expires_unix_seconds") val expires: Long,
    @SerialName("wire_version") val wireVersion: Int = 1,
) { override fun toString() = "Invitation(<redacted>)" }
class DomainError(val code: String) : IllegalArgumentException(code)
fun demand(condition: Boolean, code: String) { if (!condition) throw DomainError(code) }
