package org.itom.domain

/** An existing IMAP/TCP socket remains bound to its original physical network. */
fun requiresTransportRebind(attachedHandle: Long, selectedHandle: Long?, lostUnderlying: Boolean): Boolean =
    attachedHandle > 0L && selectedHandle != null && selectedHandle > 0L &&
        (selectedHandle != attachedHandle || lostUnderlying)

/** Short transient queues must not restart the transport. Rebind a silent peer
 * only when authenticated inbound traffic has stopped while work is pending. */
fun requiresTransportStallRecovery(inboundAgeMs: Long, pendingPackets: Int, underlyingAvailable: Boolean): Boolean =
    underlyingAvailable && (inboundAgeMs >= 30_000 && pendingPackets >= 3 ||
        inboundAgeMs >= 60_000 && pendingPackets > 0)


// Socket readiness is not authenticated application progress. The extreme
// mobile trace retained 45 ready sockets with 1,807 mailbox messages waiting
// and only one session-level packet pending. Both layers must be observed.
enum class RecoveryStep { NONE, ACCOUNT, RESTART }

data class TransportProgress(
    val underlyingAvailable: Boolean,
    val streams: Int,
    val recentDemand: Boolean,
    val appAckAgeMs: Long,
    val appPayloadAgeMs: Long,
    val oldestPendingAgeMs: Long,
    val pendingPackets: Int,
    val mailboxOutstanding: Int,
    val selectiveAttempts: Long
)

fun transportRecoveryStep(p: TransportProgress): RecoveryStep {
    if (!p.underlyingAvailable || (p.streams == 0 && !p.recentDemand)) return RecoveryStep.NONE
    // Retained CONTROL messages are normal at idle. In the November 13
    // quiet-Wi-Fi recording, more than 250 reservations coexisted with 8 ms
    // IMAP commands and no outstanding application packets. A long-lived
    // TCP stream is not evidence of new delivery demand by itself.
    val overdueDelivery = p.pendingPackets > 0 && p.oldestPendingAgeMs >= 12_000
    val liveBacklog = p.recentDemand && p.mailboxOutstanding >= 128
    // A truly severe mailbox stall must still recover after the short
    // recent-demand window expires. Requiring 60 seconds of missing useful
    // delivery plus a much larger backlog avoids treating normal idle
    // CONTROL retention (roughly 255 messages in the Wi-Fi trace) as failure.
    val severeStall = p.streams > 0 && p.mailboxOutstanding >= 1_024 &&
        p.appAckAgeMs >= 60_000 && p.appPayloadAgeMs >= 60_000
    if (!overdueDelivery && !liveBacklog && !severeStall) return RecoveryStep.NONE
    // Allow account repair for a long payload stall even if an occasional ACK
    // arrives, but keep recently delivered ACKs from causing false recovery
    val deliveryStalled = minOf(p.appAckAgeMs, p.appPayloadAgeMs) >= 15_000 ||
        (p.appPayloadAgeMs >= 45_000 && p.appAckAgeMs >= 10_000)
    if (!deliveryStalled) return RecoveryStep.NONE
    // Avoid simultaneous redials on a weak cellular uplink. Last resort only
    // after several bounded attempts and three minutes of no useful delivery.
    return if (minOf(p.appAckAgeMs, p.appPayloadAgeMs) >= 180_000 && p.selectiveAttempts >= 7)
        RecoveryStep.RESTART else RecoveryStep.ACCOUNT
}
