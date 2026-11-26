package org.itom.domain

import kotlin.test.*

class NetworkHandoverTest {
    @Test fun fieldTraceWifiMobileWifiRequestsPerAccountRedial() {
        // 23:00:17 selected_mobile after selected_none; 23:01:27 selected_wifi.
        val wifi = 101L
        val mobile = 202L
        assertFalse(requiresTransportRebind(wifi, null, true))
        assertTrue(requiresTransportRebind(wifi, mobile, true))
        assertTrue(requiresTransportRebind(mobile, wifi, false))
    }
    @Test fun fieldTraceStopsForwardProgressButContinuesQueuing() {
        // Trace: 214ms RTT frozen and 18->73 pending frames after 23:00:17.
        assertFalse(requiresTransportStallRecovery(15_000, 18, true))
        assertTrue(requiresTransportStallRecovery(30_000, 33, true))
        assertTrue(requiresTransportStallRecovery(60_000, 1, true))
        assertFalse(requiresTransportStallRecovery(120_000, 73, false))
        assertFalse(requiresTransportStallRecovery(120_000, 0, true))
    }
    @Test fun unchangedOrStartupNetworkDoesNotTriggerRedial() {
        assertFalse(requiresTransportRebind(0, 101, false))
        assertFalse(requiresTransportRebind(101, 101, false))
        assertTrue(requiresTransportRebind(101, 101, true))
    }
    @Test fun dev7ExtremeMobileNeverMisreadsHeartbeatAsApplicationProgress() {
        val stalled = TransportProgress(true, 13, true, 25_000, 23_000, 0, 1, 1_807, 0)
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(stalled))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(appAckAgeMs = 1_000)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(streams = 0, recentDemand = false)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(mailboxOutstanding = 20, pendingPackets = 0)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(underlyingAvailable = false)))
    }
    @Test fun dev7EscalatesOnlyAfterBoundedAttemptsAndNoUsefulDelivery() {
        val stalled = TransportProgress(true, 6, true, 185_000, 190_000, 28_000, 1, 200, 7)
        assertEquals(RecoveryStep.RESTART, transportRecoveryStep(stalled))
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(stalled.copy(selectiveAttempts = 5)))
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(stalled.copy(appAckAgeMs = 10_000, appPayloadAgeMs = 50_000)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(appAckAgeMs = 1_000, appPayloadAgeMs = 50_000)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(appAckAgeMs = 10_000, appPayloadAgeMs = 44_999)))
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(stalled.copy(appAckAgeMs = 10_000, appPayloadAgeMs = 45_000)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(appAckAgeMs = 10_000, appPayloadAgeMs = 50_000, mailboxOutstanding = 0, pendingPackets = 0)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(stalled.copy(appAckAgeMs = 10_000, appPayloadAgeMs = 50_000, underlyingAvailable = false)))
    }
    @Test fun dev7Fix2IdleControlRetentionIsNotATransportStall() {
        // Recorded home idle: ~255 retained messages, fast IMAP, no delivery
        // pending. Idle TCP streams must not trigger periodic socket redials.
        val idle = TransportProgress(true, 2, false, 120_000, 120_000, 0, 0, 255, 0)
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(idle))
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(idle.copy(mailboxOutstanding = 1_807)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(idle.copy(mailboxOutstanding = 1_807, appAckAgeMs = 59_999)))
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(idle.copy(recentDemand = true)))
        // A packet overdue for delivery still warrants recovery after the
        // recent-demand window ends, even with a small mailbox backlog.
        assertEquals(RecoveryStep.ACCOUNT, transportRecoveryStep(idle.copy(pendingPackets = 1, oldestPendingAgeMs = 20_000, mailboxOutstanding = 1)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(idle.copy(pendingPackets = 1, oldestPendingAgeMs = 11_999, mailboxOutstanding = 1)))
        assertEquals(RecoveryStep.NONE, transportRecoveryStep(idle.copy(pendingPackets = 1, oldestPendingAgeMs = 20_000, appAckAgeMs = 1_000)))
    }
}
