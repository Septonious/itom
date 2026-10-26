package org.itom.domain

import kotlin.test.*

class StartupEvidenceTest {
    @Test fun failureRetainsLastRealStageAndSelectedCountsWithoutInventingReadyLines() {
        val raw = """{"StartupStage":"IMAP","SelectedLines":4,"TotalLines":30,"ElapsedMS":2500,"Password":"private","Response":"private"}"""
        val report = startupEvidence(raw, StartupOutcome.FAILED, "V2_STARTUP_IMAP", 99)
        assertEquals("IMAP", report.field("StartupStage"))
        assertEquals("4", report.field("SelectedLines"))
        assertEquals("FAILED", report.field("Outcome"))
        assertEquals("V2_STARTUP_IMAP", report.field("StartupFailure"))
        assertFalse(report.toString().contains("private"))
        assertFalse(report.containsKey("ActiveLines"))
    }
    @Test fun stoppedReportRemainsExportableAndUntrustedFieldsCannotBecomeDiagnostics() {
        val report = startupEvidence("""{"StartupStage":"secret","SelectedLines":31,"TotalLines":30,"ElapsedMS":-1,"ConfigHash":"password"}""", StartupOutcome.STOPPED, "private-provider-response", 100)
        assertEquals("STOPPED", report.field("Outcome"))
        assertEquals("TRANSPORT_FAILED", report.field("StartupFailure"))
        assertNull(report.field("StartupStage"))
        assertNull(report.field("SelectedLines"))
        assertNull(report.field("ConfigHash"))
        assertNull(report.field("ElapsedMS"))
        assertEquals(report, startupEvidence(report.toString(), StartupOutcome.STOPPED, "TRANSPORT_FAILED", 100))
    }
}
