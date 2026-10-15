package org.itom.domain

import kotlinx.serialization.json.*

enum class DiagnosticLevel { INFO, WARNING, ERROR }

/** Codes are local classifications, never arbitrary IMAP provider responses. */
fun diagnosticLevel(code: String): DiagnosticLevel = when {
    code.isBlank() || code in setOf("OK", "CANCELED", "SESSION_SUPERSEDED") -> DiagnosticLevel.INFO
    code in setOf("TIMEOUT", "TRANSPORT_TIMEOUT", "TCP_OPEN_TIMEOUT", "IMAP_CONNECT_FAILED",
        "IO_OR_PROTOCOL_ERROR", "IMAP_APPEND_OUTCOME_UNKNOWN", "IMAP_LIMIT", "CONTROL_QUEUE_LIMIT",
        "RECEIPT_WINDOW_LIMIT", "REMOTE_GC_INCOMPLETE", "JOURNAL_RECOVERY_REQUIRED",
        "COORDINATOR_NOT_READY", "CARRIER_REATTACH", "CARRIER_REKEY", "LANE_ROTATION_REQUIRED",
        "RATE_LIMIT", "READER_STALE", "OPEN_QUARANTINE", "GRANTS") -> DiagnosticLevel.WARNING
    else -> DiagnosticLevel.ERROR
}

fun JsonObject.field(name: String): String? = (get(name) as? JsonPrimitive)?.contentOrNull
fun JsonObject.objectField(name: String): JsonObject? = get(name) as? JsonObject

data class TransportDiagnostics(val root: JsonObject, val lines: List<JsonObject>) {
    // A historic Stage="ready" is not evidence of readiness in this sample.
    val reportedActive: Int? get() = root.field("ActiveLines")?.toIntOrNull()
    val lineActive: Int? get() = if (lines.isNotEmpty() && lines.all { it.field("ActiveLines")?.toIntOrNull() != null })
        lines.sumOf { it.field("ActiveLines")!!.toInt() } else null
    fun consistent(metrics: Metrics): Boolean = reportedActive == metrics.activeLines &&
        root.field("TotalLines")?.toIntOrNull() == metrics.totalLines && lineActive == metrics.activeLines
    companion object {
        fun parse(text: String): TransportDiagnostics {
            val root = runCatching { Codec.json.parseToJsonElement(text) as? JsonObject }.getOrNull() ?: JsonObject(emptyMap())
            val lines = (root["Lines"] as? JsonArray)?.mapNotNull { it as? JsonObject } ?: emptyList()
            return TransportDiagnostics(root, lines)
        }
    }
}
