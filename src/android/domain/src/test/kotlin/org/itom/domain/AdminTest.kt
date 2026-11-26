package org.itom.domain

import kotlin.test.*

class AdminTest {
    @Test fun roleNeedsVerifiedServerStatusAndIdentity() {
        assertFalse(AdminCodec.view("""{"profile_name":"admin"}""").authorized)
        assertFalse(AdminCodec.view("""{"authorized":true}""").authorized)
        assertFalse(AdminCodec.view("""{"authorized":true,"status":{"admin":false,"current_uid":"12345678"}}""").authorized)
        assertFalse(AdminCodec.view("""{"authorized":true,"status":{"admin":true,"current_uid":"01234567"}}""").authorized)
        assertTrue(AdminCodec.view("""{"authorized":true,"status":{"admin":true,"current_uid":"12345678"}}""").authorized)
    }
    @Test fun malformedOrDuplicateRoleCannotEnableControls() {
        assertFalse(AdminCodec.view("""{"authorized":false,"authorized":true}""").authorized)
        assertFalse(AdminCodec.view("""{"authorized":"true"}""").authorized)
        assertFalse(AdminCodec.view("x".repeat((1 shl 20) + 1)).authorized)
    }
    @Test fun leaseUsesServerTimeAndElapsedTimeRatherThanPhoneClock() {
        val pending = AdminPending("id", state = "observing", loss_deadline_ms = 120000, server_time_ms = 60000)
        assertEquals(60L, AdminCodec.trialSeconds(pending, 0))
        assertEquals(1L, AdminCodec.trialSeconds(pending, 59999))
        assertEquals(0L, AdminCodec.trialSeconds(pending, 70000))
        assertTrue(pending.active)
        assertFalse(pending.copy(state = "committed").active)
    }
    @Test fun noticesRespectUtf8ByteLimitAndControlCharacters() {
        assertTrue(AdminCodec.messageValid("&cHello &aWorld\nSecond line"))
        assertTrue(AdminCodec.messageValid("я".repeat(1024)))
        assertFalse(AdminCodec.messageValid("я".repeat(1025)))
        assertFalse(AdminCodec.messageValid("bad\u0000notice"))
        assertFalse(AdminCodec.messageValid("bad\u001bnotice"))
    }
}
