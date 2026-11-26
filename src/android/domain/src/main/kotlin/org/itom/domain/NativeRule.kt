package org.itom.domain

/** Canonicalizes legacy bare rules and the restored CSQTT syntax. The Go
 * classifier also validates the referenced offline categories before saving. */
object NativeRule {
    fun canonical(input: String): String {
        val raw = input.trim()
        demand(raw.isNotEmpty() && raw.length <= 1024, "INVALID_RULE")
        val lower = raw.lowercase(java.util.Locale.ROOT)
        return when {
            lower.startsWith("domain:") || lower.startsWith("full:") -> {
                val kind = lower.substringBefore(':')
                val host = Addresses.domain(raw.substringAfter(':').trim().removePrefix("."))
                "$kind:$host"
            }
            lower.startsWith("ip:") -> {
                val value = raw.substringAfter(':').trim()
                demand(Addresses.ip(value) != null || '/' in value, "INVALID_IP")
                "ip:" + CompiledRule.parse(value).source
            }
            lower.startsWith("geosite:") -> {
                val name = lower.substringAfter(':').trim()
                demand(name.matches(Regex("[a-z0-9_@.-]{1,128}")), "INVALID_GEOSITE")
                "geosite:$name"
            }
            lower.startsWith("geoip:") -> {
                demand(lower.substringAfter(':').trim() == "ru", "INVALID_GEOIP")
                "geoip:ru"
            }
            else -> {
                val rule = CompiledRule.parse(raw).source
                if ('/' in rule || Addresses.ip(rule) != null) "ip:$rule"
                else "domain:" + rule.removePrefix(".")
            }
        }
    }
    fun canonicalList(entries: List<String>): List<String> =
        entries.map(String::trim).filter { it.isNotEmpty() && !it.startsWith("#") }.map(::canonical).distinct().also {
            demand(it.size <= 256, "TOO_MANY_RULES")
        }
}
