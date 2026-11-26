package org.itom.domain

import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName

/** A rule-only document: no credentials, application inventory or profile identifiers. */
@Serializable
data class RoutingRuleDocument(@SerialName("schema_version") val version: Int, val rules: AddressRules)

object RoutingRulesExchange {
    const val LIMIT = 1024 * 1024
    fun canonical(r: AddressRules) = AddressRules(NativeRule.canonicalList(r.VPN), NativeRule.canonicalList(r.Block), NativeRule.canonicalList(r.Direct))
    fun encode(r: AddressRules): String = Codec.json.encodeToString(RoutingRuleDocument.serializer(), RoutingRuleDocument(1, canonical(r)))
        .also { Codec.preflight(it, LIMIT) }
    fun decode(text: String): AddressRules {
        Codec.preflight(text, LIMIT)
        val doc = Codec.json.decodeFromString(RoutingRuleDocument.serializer(), text)
        demand(doc.version == 1, "INVALID_RULE_DOCUMENT")
        return canonical(doc.rules)
    }
}
