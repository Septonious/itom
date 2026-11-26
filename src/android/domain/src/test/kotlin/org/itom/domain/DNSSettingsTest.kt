package org.itom.domain

import kotlinx.serialization.json.*
import kotlin.test.*
import org.junit.Test

class DNSSettingsTest {
    @Test fun finalDefaultsAndExistingSettingsUseDirectDNS() {
        val expected = DNSSettings(primary = "31.131.251.83", backup = "185.22.234.229")
        assertEquals(expected, AppSettings().dnsResolver)
        assertFalse(expected.itomFallback)
        for (version in listOf(3, 4, 5)) {
            val old = Codec.settings("""{"schema_version":$version,"traffic":{"dns":"server-tcp"}}""")
            assertEquals(expected, old.dnsResolver)
            assertEquals("configured", old.traffic.dns)
            assertFalse(old.traffic.automaticDirectFallback)
        }
    }

    @Test fun customResolversAndIMAPFallbackSurviveVaultRoundtrip() {
        for (fallback in listOf(false, true)) {
            val dns = DNSSettings("192.0.2.53", "192.0.2.54", itomFallback = fallback)
            val original = VaultDocument(settings = AppSettings(dnsResolver = dns), revision = 19)
            val decoded = Codec.vault(Codec.encode(original))
            assertEquals(original.settings, decoded.settings)
            assertEquals(19L, decoded.revision)
            val raw = Codec.json.encodeToJsonElement(dns).jsonObject
            assertEquals(dns.primary, raw.getValue("primary").jsonPrimitive.content)
            assertEquals(fallback, raw.getValue("itom_fallback").jsonPrimitive.boolean)
            assertEquals(setOf("primary", "backup", "itom_fallback"), raw.keys)
        }
    }

    @Test fun invalidOrMissingBackupCannotEnableIMAPFallback() {
        val defaults = DNSSettings()
        for (dns in listOf(defaults.copy(primary = "resolver.example"), defaults.copy(primary = ""),
            defaults.copy(primary = "0.0.0.0"), defaults.copy(backup = defaults.primary),
            defaults.copy(backup = "", itomFallback = true))) {
            assertFailsWith<DomainError> { Validation.settings(AppSettings(dnsResolver = dns)) }
        }
        Validation.settings(AppSettings(dnsResolver = defaults.copy(backup = "")))
    }
}
