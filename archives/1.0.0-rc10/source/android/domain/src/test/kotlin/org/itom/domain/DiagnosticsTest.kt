package org.itom.domain

import kotlin.test.*

class DiagnosticsTest {
    @Test fun historicReadyAndMalformedEntriesCannotConcealMissingLines() {
        val text = """{"ActiveLines":14,"TotalLines":20,"Lines":[{"Stage":"ready","Ready":false,"ActiveLines":0},{"Stage":"ready","Ready":true,"ActiveLines":14},null]}"""
        val view = TransportDiagnostics.parse(text)
        assertEquals(2, view.lines.size)
        assertEquals("false", view.lines.first().field("Ready"))
        assertTrue(view.consistent(Metrics(activeLines = 14, totalLines = 20)))
        assertFalse(view.consistent(Metrics(activeLines = 20, totalLines = 20)))
        assertNull(TransportDiagnostics.parse("""{"Lines":[{"Stage":"ready"}]}""").lineActive)
        for (malformed in listOf("not json", "null", "[]", """{"Lines":123}""")) {
            assertTrue(TransportDiagnostics.parse(malformed).lines.isEmpty())
        }
    }
    @Test fun recoveringWarningsDifferFromTerminalCodes() {
        assertEquals(DiagnosticLevel.WARNING, diagnosticLevel("TIMEOUT"))
        assertEquals(DiagnosticLevel.WARNING, diagnosticLevel("IMAP_APPEND_OUTCOME_UNKNOWN"))
        assertEquals(DiagnosticLevel.WARNING, diagnosticLevel("IMAP_MAILBOX_STATE_CHANGED"))
        assertEquals(DiagnosticLevel.ERROR, diagnosticLevel("IMAP_TLS_FAILED"))
        assertEquals(DiagnosticLevel.ERROR, diagnosticLevel("JOURNAL_CORRUPT"))
        assertEquals(DiagnosticLevel.INFO, diagnosticLevel("CANCELED"))
        assertEquals(DiagnosticLevel.ERROR, diagnosticLevel("NEW_UNRECOGNIZED_CODE"))
    }
}
