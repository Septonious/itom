package org.itom.domain

import java.net.IDN
import java.util.Locale

/** Pure classifier. It does not resolve DNS or create any sockets. */
object Addresses {
    fun domain(input: String): String {
        val value = input.trim().removeSuffix(".")
        demand(value.isNotEmpty() && value.length <= 253 && !value.any { it.isWhitespace() || it in "/:@*[]\\" }, "INVALID_DOMAIN")
        val ascii = try { IDN.toASCII(value, IDN.USE_STD3_ASCII_RULES).lowercase(Locale.ROOT) }
        catch (_: IllegalArgumentException) { throw DomainError("INVALID_DOMAIN") }
        demand(ascii.length <= 253 && ascii.split('.').all { it.length in 1..63 && !it.startsWith('-') && !it.endsWith('-') }, "INVALID_DOMAIN")
        demand(!ascii.all { it.isDigit() || it == '.' }, "INVALID_DOMAIN")
        return ascii
    }

    fun ip(input: String): ByteArray? {
        if (input.contains('%') || input.contains('[') || input.contains(']')) return null
        if (!input.contains(':')) return ipv4(input)
        if (!input.all { it in "0123456789abcdefABCDEF:." }) return null
        var value = input
        if (value.contains('.')) {
            val colon = value.lastIndexOf(':')
            val tail = ipv4(value.substring(colon + 1)) ?: return null
            val a = ((tail[0].toInt() and 255) shl 8) or (tail[1].toInt() and 255)
            val b = ((tail[2].toInt() and 255) shl 8) or (tail[3].toInt() and 255)
            value = value.substring(0, colon + 1) + a.toString(16) + ":" + b.toString(16)
        }
        val parts = value.split("::")
        if (parts.size > 2) return null
        fun groups(part: String): List<Int>? {
            if (part.isEmpty()) return emptyList()
            val items = part.split(':')
            if (items.any { it.length !in 1..4 }) return null
            return items.map { it.toIntOrNull(16) ?: return null }
        }
        val left = groups(parts[0]) ?: return null
        val right = if (parts.size == 2) groups(parts[1]) ?: return null else emptyList()
        val count = left.size + right.size
        if (parts.size == 1 && count != 8 || parts.size == 2 && count >= 8) return null
        val words = left + List(if (parts.size == 2) 8 - count else 0) { 0 } + right
        return ByteArray(16) { i -> (if (i % 2 == 0) words[i / 2] ushr 8 else words[i / 2]).toByte() }
    }

    private fun ipv4(value: String): ByteArray? {
        val parts = value.split('.')
        if (parts.size != 4) return null
        val words = parts.map {
            if (it.isEmpty() || it.length > 3 || !it.all { c -> c in '0'..'9' } || it.length > 1 && it.startsWith('0')) return null
            val number = it.toIntOrNull() ?: return null
            if (number !in 0..255) return null
            number
        }
        return ByteArray(4) { words[it].toByte() }
    }
}

class IpNetwork(private val bytes: ByteArray, private val prefix: Int) {
    fun contains(address: ByteArray): Boolean {
        if (bytes.size != address.size) return false
        val full = prefix / 8
        if ((0 until full).any { bytes[it] != address[it] }) return false
        val bits = prefix % 8
        if (bits == 0) return true
        val mask = (255 shl (8 - bits)) and 255
        return (bytes[full].toInt() and mask) == (address[full].toInt() and mask)
    }
}

class CompiledRule(val source: String, private val suffix: String? = null, private val network: IpNetwork? = null) {
    fun matches(domain: String?, ip: ByteArray?): Boolean =
        (suffix != null && domain != null && (domain == suffix || domain.endsWith(".$suffix"))) ||
        (network != null && ip != null && network.contains(ip))

    companion object {
        fun parse(input: String): CompiledRule {
            val value = input.trim()
            demand(value.isNotEmpty() && value.length <= 256, "INVALID_RULE")
            if ('/' in value) {
                val parts = value.split('/')
                demand(parts.size == 2, "INVALID_CIDR")
                val address = Addresses.ip(parts[0]) ?: throw DomainError("INVALID_CIDR")
                demand(parts[1].matches(Regex("[0-9]{1,3}")), "INVALID_CIDR")
                val prefix = parts[1].toInt()
                demand(prefix in 0..address.size * 8, "INVALID_CIDR")
                return CompiledRule(value, network = IpNetwork(address, prefix))
            }
            Addresses.ip(value)?.let { return CompiledRule(value, network = IpNetwork(it, it.size * 8)) }
            demand(':' !in value && !value.all { it.isDigit() || it == '.' }, "INVALID_IP")
            val bare = if (value.startsWith('.')) value.drop(1) else value
            val host = Addresses.domain(bare)
            if (value.startsWith('.')) demand('.' !in host, "INVALID_TLD")
            return CompiledRule(if (value.startsWith('.')) ".$host" else host, suffix = host)
        }
    }
}

data class RouteDecision(val action: RouteAction, val matched: String?, val appBypass: Boolean = false)
class Router(policy: RoutingPolicy) {
    private val groups = listOf(RouteAction.VPN, RouteAction.BLOCK, RouteAction.DIRECT).map { action ->
        action to policy.rules.entries(action).map(CompiledRule::parse)
    }
    fun decide(host: String? = null, address: String? = null, packageName: String? = null, apps: AppPolicy = AppPolicy()): RouteDecision {
        if (packageName != null && !apps.captures(packageName)) return RouteDecision(RouteAction.DIRECT, null, true)
        val ip = address?.let { Addresses.ip(it) ?: throw DomainError("INVALID_IP") } ?: host?.let(Addresses::ip)
        val domain = host?.takeIf { Addresses.ip(it) == null }?.let(Addresses::domain)
        groups.forEach { (action, rules) -> rules.firstOrNull { it.matches(domain, ip) }?.let { return RouteDecision(action, it.source) } }
        return RouteDecision(RouteAction.VPN, null)
    }
}

object AppSelection {
    fun validate(policy: AppPolicy, catalog: List<InstalledApp>) {
        demand(policy.packages.size <= 4096 && policy.packages.distinct().size == policy.packages.size, "INVALID_APP_LIST")
        demand(policy.packages.all { it.matches(Regex("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z0-9_]+)*")) }, "INVALID_PACKAGE")
        if (policy.mode == AppMode.ALL) { demand(policy.packages.isEmpty(), "ALL_REQUIRES_EMPTY_LIST"); return }
        demand(policy.mode != AppMode.INCLUDE_SELECTED || policy.packages.isNotEmpty(), "EMPTY_INCLUDE")
        val installed = catalog.map { it.packageName }.toSet()
        demand(policy.packages.all { it in installed }, "APP_NOT_INSTALLED")
        val selected = policy.packages.toSet()
        catalog.groupBy { it.uid }.values.forEach { group ->
            val count = group.count { it.packageName in selected }
            demand(count == 0 || count == group.size, "SHARED_UID_PARTIAL_SELECTION")
        }
    }
    fun toggle(selected: Set<String>, app: InstalledApp, catalog: List<InstalledApp>): Set<String> {
        val group = catalog.filter { it.uid == app.uid }.map { it.packageName }.toSet()
        return if (app.packageName in selected) selected - group else selected + group
    }
}
