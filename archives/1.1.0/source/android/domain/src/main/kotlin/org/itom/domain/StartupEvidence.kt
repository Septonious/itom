package org.itom.domain

import kotlinx.serialization.json.*

enum class StartupOutcome { STARTING, CONNECTED, FAILED, STOPPED }

/** Small, allowlisted evidence survives Stop/process death even with tracing off. */
fun startupEvidence(raw: String, outcome: StartupOutcome, failure: String?, sampledAt: Long): JsonObject {
    val root = TransportDiagnostics.parse(raw).root
    return buildJsonObject {
        put("Outcome", outcome.name)
        put("SampledAtMS", sampledAt.coerceAtLeast(0))
        root.field("Revision")?.takeIf { it.matches(Regex("[a-zA-Z0-9._-]{1,80}")) }?.let { put("Revision", it) }
        root.field("StartupStage")?.takeIf { it in setOf("PROFILE", "WALLET", "IMAP", "RENDEZVOUS", "REQUEST", "INVITATION", "NOISE", "ROSTER", "RUNTIME", "PERMISSION", "PATH", "READY") }?.let { put("StartupStage", it) }
        root.field("FailureReason")?.takeIf { it in startupFailureReasons }?.let { put("FailureReason", it) }
        root.field("RequestKeywordBytes")?.toIntOrNull()?.takeIf { it in setOf(16, 31, 59, 103) }?.let { put("RequestKeywordBytes", it) }
        root.field("ConfigHash")?.takeIf { it.matches(Regex("[a-f0-9]{64}")) }?.let { put("ConfigHash", it) }
        val total = root.field("TotalLines")?.toIntOrNull()?.takeIf { it in 0..4096 }
        if (total != null) {
            put("TotalLines", total)
            root.field("SelectedLines")?.toIntOrNull()?.takeIf { it in 0..total }?.let { put("SelectedLines", it) }
        }
        root.field("ElapsedMS")?.toLongOrNull()?.takeIf { it in 0L..3_600_000L }?.let { put("ElapsedMS", it) }
        if (failure != null) put("StartupFailure", nativeFailureCode(failure))
    }
}

private val startupFailureReasons = setOf(
    "CANCELED", "TIMEOUT", "JOURNAL_CONFIG_MISMATCH", "UIDPLUS_UNSUPPORTED",
    "PERMANENT_KEYWORDS_UNSUPPORTED", "IMAP_TAGGED_REFUSAL", "PROTOCOL_OR_RETAINED_BODY",
    "CAPACITY_OR_INVENTORY_BOUND", "STORAGE_QUOTA_EXHAUSTED", "JOURNAL_IO_UNCERTAIN", "JOURNAL_CORRUPT", "WALLET_IO_UNCERTAIN", "WALLET_STATE_INVALID", "RUNTIME_STOPPED", "ROSTER_REVISION_REQUIRED", "SELECTED_LANE_CLOSED", "IMAP_CONNECT_FAILED", "IMAP_TLS_FAILED",
    "IMAP_GREETING_FAILED", "IMAP_LOGIN_FAILED", "IMAP_CAPABILITY_FAILED",
    "IMAP_SELECT_FAILED", "IMAP_CREATE_FAILED", "LOCAL_OR_SESSION_FAILURE",
)
