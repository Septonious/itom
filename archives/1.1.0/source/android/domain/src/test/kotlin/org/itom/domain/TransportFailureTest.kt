package org.itom.domain

import org.junit.Test
import kotlin.test.assertEquals

class TransportFailureTest {
    @Test fun selectedMailConnectionsAreShownWithoutClaimingVPNReadiness() {
        assertEquals("Connecting mail lines: 8/20 · Tap to cancel",
            startupStatus("""{"StartupStage":"IMAP","SelectedLines":8,"TotalLines":20}"""))
        assertEquals("Contacting server · Tap to cancel", startupStatus("""{"StartupStage":"RENDEZVOUS"}"""))
    }

    @Test fun unknownStartupDataNeverBecomesInterfaceText() {
        assertEquals("Tap to cancel", startupStatus("""{"StartupStage":"provider secret"}"""))
        assertEquals("Connecting mail lines · Tap to cancel",
            startupStatus("""{"StartupStage":"IMAP","SelectedLines":21,"TotalLines":20}"""))
    }
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
