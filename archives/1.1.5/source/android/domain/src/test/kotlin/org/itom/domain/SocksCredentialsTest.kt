package org.itom.domain

import org.junit.Test
import kotlin.test.*

class SocksCredentialsTest {
    @Test fun generatesMissingCredentialsAndKeepsThemAcrossToggles() {
        val generated = SocksDraft("1080", false, "", "", 1).withAuthentication(true)
        assertTrue(generated.authentication)
        assertTrue(generated.username.startsWith("itom_"))
        assertEquals(32, generated.password.length)
        assertTrue(generated.password.all { it.isLetterOrDigit() || it == '-' || it == '_' })
        assertEquals(generated, generated.withAuthentication(false).withAuthentication(true))
        assertEquals("SocksDraft(<redacted>)", generated.toString())
    }
    @Test fun preservesEachExistingFieldAndDoesNotGenerateWhenDisabled() {
        val blank = SocksDraft("1080", false, "", "", 1)
        assertEquals(blank, blank.withAuthentication(false))
        assertEquals("chosen", blank.copy(username = "chosen").withAuthentication(true).username)
        assertEquals("chosen", blank.copy(password = "chosen").withAuthentication(true).password)
        assertNotEquals(blank.withAuthentication(true).password, blank.withAuthentication(true).password)
    }
}
