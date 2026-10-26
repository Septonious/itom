package org.itom.domain

import org.junit.Test
import kotlin.test.assertEquals

class TransportFailureTest {
    @Test fun startupPhasesSurviveTheNativeBoundary() {
        for (stage in listOf("PROFILE", "WALLET", "IMAP", "RENDEZVOUS", "NOISE", "ROSTER", "RUNTIME", "PERMISSION")) {
            val code = "V2_STARTUP_$stage"
            assertEquals(code, nativeFailureCode(code))
        }
    }

    @Test fun arbitraryNativeMessagesNeverEnterDiagnosticsOrUI() {
        for (message in listOf(null, "", "provider response with a password", "V2_STARTUP_IMAP secret", "V2_STARTUP_UNKNOWN")) {
            assertEquals("TRANSPORT_FAILED", nativeFailureCode(message))
        }
    }
}
