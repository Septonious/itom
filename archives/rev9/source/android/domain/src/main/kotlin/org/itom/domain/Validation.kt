package org.itom.domain

import java.util.Base64

object Validation {
    fun validDialIP(value: String): Boolean {
        if (value.isEmpty()) return true
        var bytes = Addresses.ip(value) ?: return false
        if (bytes.size == 16 && bytes.take(10).all { it == 0.toByte() } && bytes[10] == (-1).toByte() && bytes[11] == (-1).toByte()) bytes = bytes.copyOfRange(12, 16)
        if (bytes.all { it == 0.toByte() }) return false
        return if (bytes.size == 4) (bytes[0].toInt() and 255) !in 224..239 && !bytes.all { it == (-1).toByte() }
        else (bytes[0].toInt() and 255) != 255
    }
    fun profile(p: ServerProfile) {
        demand(p.schemaVersion == 2, "PROFILE_VERSION")
        demand(p.profileId.matches(Regex("[A-Za-z0-9_-]{1,64}")), "PROFILE_ID")
        demand(p.name.isNotBlank() && p.name.length <= 80 && p.name.none(Char::isISOControl), "PROFILE_NAME")
        demand(p.serverId.matches(Regex("[a-f0-9]{32}")), "SERVER_ID")
        demand(p.userId.matches(Regex("[1-9][0-9]{7}")), "USER_ID")
        demand(p.providerId.matches(Regex("[a-z0-9][a-z0-9_-]{0,63}")), "PROVIDER_ID")
        demand(p.encryptionKeyRef.matches(Regex("keystore:[A-Za-z0-9_-]{1,96}")), "KEY_REF")
        demand(p.mailboxNamespace.matches(Regex("[A-Za-z0-9][A-Za-z0-9_./-]{1,127}")) &&
            p.mailboxNamespace.split('/').none { it.isEmpty() || it == "." || it == ".." }, "FOLDER_NAMESPACE")
        demand(p.transportProfile in setOf("balanced-lab-v1", "imap-tcp-v1"), "TRANSPORT_PROFILE")
        demand(p.accounts.isNotEmpty() && p.accounts.any { it.enabled }, "ACCOUNT_COUNT")
        val resources = p.transportResources
        demand(resources.layout in setOf("auto", "mixed", "split") && resources.dataShards in setOf(1, 2, 4) &&
            resources.participants in 1..2 && resources.accountSessions in (2 * resources.participants)..128 &&
            resources.endpointConnections in 2..4096 && resources.loginParallelism in 1..32, "TRANSPORT_RESOURCES")
        demand(p.accounts.map { it.id }.distinct().size == p.accounts.size, "DUPLICATE_ACCOUNT")
        demand(p.accounts.map { it.credentialRef }.distinct().size == p.accounts.size, "DUPLICATE_CREDENTIAL_REF")
        p.accounts.forEach {
            demand(it.id.matches(Regex("[A-Za-z0-9_-]{1,64}")), "ACCOUNT_ID")
            demand(it.name.isNotBlank() && it.name.length <= 320 && it.name.none(Char::isISOControl), "ACCOUNT_NAME")
            demand(it.providerId == p.providerId, "MIXED_PROVIDERS")
            demand(it.port in 1..65535 && it.tls in setOf("imaps", "starttls") && it.verifyCertificate, "TLS_REQUIRED")
            val validHost = it.host == it.host.trim() && it.host.length <= 253 && try {
                Addresses.ip(it.host) != null || Addresses.domain(it.host).isNotEmpty()
            } catch (_: DomainError) { false }
            demand(validHost, "IMAP_HOST")
            demand(validDialIP(it.dialIP), "IMAP_DIAL_IP_INVALID")
            demand(it.credentialRef.matches(Regex("keystore:[A-Za-z0-9_-]{1,96}")), "CREDENTIAL_REF")
        }
    }
    fun settings(s: AppSettings) {
        demand(s.schemaVersion == 5, "SETTINGS_VERSION")
        demand(s.networks.wifi || s.networks.mobile, "NETWORK_SELECTION_EMPTY")
        demand(s.socks.port in 1024..65535, "SOCKS_PORT")
        demand(s.traffic == TrafficPolicy(), "UNSUPPORTED_TRAFFIC_POLICY")
        demand(s.routing.schemaVersion == 1 && s.routing.scope == "global" && s.routing.defaultAction == RouteAction.VPN &&
            s.routing.priority == listOf(RouteAction.VPN, RouteAction.BLOCK, RouteAction.DIRECT), "ROUTING_PRIORITY")
        demand(s.rttThresholds.greenMax in 1..5000 && s.rttThresholds.yellowMax in s.rttThresholds.greenMax + 1..10000, "RTT_THRESHOLDS")
        demand(s.routing.priority.sumOf { s.routing.rules.entries(it).size } <= 10000, "TOO_MANY_RULES")
        s.routing.priority.forEach { action -> s.routing.rules.entries(action).forEach(CompiledRule::parse) }
        demand(s.appRouting.packages.distinct().size == s.appRouting.packages.size && s.appRouting.packages.size <= 4096, "INVALID_APP_LIST")
        demand(s.appRouting.packages.all { it.matches(Regex("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z0-9_]+)*")) }, "INVALID_PACKAGE")
        demand(s.appRouting.mode != AppMode.INCLUDE_SELECTED || s.appRouting.packages.isNotEmpty(), "EMPTY_INCLUDE")
        demand(s.appRouting.mode != AppMode.ALL || s.appRouting.packages.isEmpty(), "ALL_REQUIRES_EMPTY_LIST")
    }
    private fun credential(c: MailCredential) {
        demand(c.username.isNotBlank() && c.username.length <= 512 && c.password.isNotEmpty() && c.password.length <= 4096, "CREDENTIAL_FORMAT")
        demand((c.username + c.password).none { it == '\r' || it == '\n' || it == '\u0000' }, "CREDENTIAL_FORMAT")
    }
    private fun key(k: PairSecret) {
        demand(k.pairId.matches(Regex("[a-f0-9]{32}")), "PAIR_ID")
        val bytes = try { Base64.getDecoder().decode(k.pskBase64) } catch (_: IllegalArgumentException) { throw DomainError("PSK_FORMAT") }
        try { demand(bytes.size == 32 && Base64.getEncoder().encodeToString(bytes) == k.pskBase64, "PSK_FORMAT") } finally { bytes.fill(0) }
    }
    fun invitation(i: Invitation, nowSeconds: Long) {
        demand(i.schemaVersion == 2 && i.wireVersion in 1..2, "INVITATION_VERSION")
        demand(i.expires > nowSeconds, "INVITATION_EXPIRED")
        profile(i.profile)
        key(PairSecret(i.pairId, i.pskBase64))
        demand(i.credentials.keys == i.profile.accounts.map { it.credentialRef }.toSet(), "INVITATION_CREDENTIALS")
        i.credentials.values.forEach(::credential)
    }
    fun vault(v: VaultDocument) {
        demand(v.version == 2 && v.revision >= 0, "VAULT_VERSION")
        demand(v.profiles.size <= 64 && v.profiles.map { it.profileId }.distinct().size == v.profiles.size, "PROFILE_LIMIT")
        demand(v.profiles.map { it.serverId }.distinct().size == v.profiles.size, "DUPLICATE_SERVER")
        demand(v.selectedProfileId == null || v.profiles.any { it.profileId == v.selectedProfileId }, "SELECTED_PROFILE_MISSING")
        demand(v.incompleteProfileIds.all { id -> v.profiles.any { it.profileId == id } }, "INCOMPLETE_PROFILE_MISSING")
        v.profiles.forEach(::profile); settings(v.settings)
        if (v.settings.socks.authentication) demand(v.socksCredential != null, "SOCKS_CREDENTIALS")
        v.socksCredential?.let { c ->
            demand(c.username.toByteArray(Charsets.UTF_8).size in 1..255 && c.password.toByteArray(Charsets.UTF_8).size in 1..255 &&
                (c.username + c.password).none { it == '\u0000' || it == '\r' || it == '\n' }, "SOCKS_CREDENTIALS")
        }
        val complete = v.profiles.filter { it.profileId !in v.incompleteProfileIds }
        val refs = v.profiles.flatMap { p -> p.accounts.map { it.credentialRef } }
        val requiredRefs = complete.flatMap { p -> p.accounts.map { it.credentialRef } }.toSet()
        // Incomplete profiles may omit secrets, but any stored values must be valid and unshared.
        demand(refs.distinct().size == refs.size && v.credentials.keys.containsAll(requiredRefs) && refs.toSet().containsAll(v.credentials.keys), "VAULT_CREDENTIALS")
        val keyRefs = v.profiles.map { it.encryptionKeyRef }
        demand(keyRefs.distinct().size == keyRefs.size && v.keys.keys.containsAll(complete.map { it.encryptionKeyRef }) && keyRefs.toSet().containsAll(v.keys.keys), "VAULT_KEYS")
        v.credentials.values.forEach(::credential); v.keys.values.forEach(::key)
    }
}
