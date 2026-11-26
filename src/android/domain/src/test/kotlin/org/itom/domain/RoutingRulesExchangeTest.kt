package org.itom.domain

import kotlin.test.*

class RoutingRulesExchangeTest {
    @Test fun roundTrip() {
        val rules = AddressRules(listOf("example.com", "example.com"), listOf("ads.example.com"), listOf("192.0.2.0/24"))
        val doc = RoutingRulesExchange.encode(rules)
        assertEquals(RoutingRulesExchange.canonical(rules), RoutingRulesExchange.decode(doc))
    }
    @Test fun rejectsInvalidDocuments() {
        assertFails { RoutingRulesExchange.decode("""{"schema_version":2,"rules":{}}""") }
        assertFails { RoutingRulesExchange.decode("""{"schema_version":1,"schema_version":1,"rules":{}}""") }
        assertFails { RoutingRulesExchange.decode("""{"schema_version":1,"rules":{},"credentials":{}}""") }
    }
}
