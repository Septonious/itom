package org.itom.domain

/** Only application-defined codes may cross into the UI or diagnostics. */
fun nativeFailureCode(message: String?): String = message?.takeIf {
    it in setOf("SOCKS_PORT_IN_USE", "UNDERLYING_DNS_MISSING", "UNDERLYING_DNS_INVALID",
        "NATIVE_CONFIG", "DNS_ADDRESS", "DNS_BACKUP_REQUIRED", "DNS_DUPLICATE",
        "V2_STARTUP_PROFILE", "V2_STARTUP_WALLET", "V2_STARTUP_IMAP",
        "V2_STARTUP_RENDEZVOUS", "V2_STARTUP_REQUEST", "V2_STARTUP_INVITATION", "V2_STARTUP_NOISE", "V2_STARTUP_ROSTER",
        "V2_STARTUP_RUNTIME", "V2_STARTUP_PERMISSION", "V2_STARTUP_PATH", "V2_PROVIDER_UIDPLUS_REQUIRED", "V2_PROVIDER_PERMANENT_KEYWORDS_REQUIRED")
} ?: "TRANSPORT_FAILED"

fun startupStatus(raw: String): String {
    val root = TransportDiagnostics.parse(raw).root
    val label = when (root.field("StartupStage")) {
        "PROFILE" -> "Checking profile"
        "WALLET" -> "Opening saved session"
        "IMAP" -> {
            val total = root.field("TotalLines")?.toIntOrNull()?.takeIf { it in 1..4096 }
            val selected = root.field("SelectedLines")?.toIntOrNull()
            if (total != null && selected != null && selected in 0..total) "Connecting mail lines: $selected/$total"
            else "Connecting mail lines"
        }
        "RENDEZVOUS" -> "Contacting server"
        "REQUEST" -> "Requesting server session"
        "INVITATION" -> "Waiting for server session"
        "NOISE" -> "Securing connection"
        "ROSTER" -> "Preparing mail lines"
        "PATH" -> "Checking data path"
        "RUNTIME", "PERMISSION" -> "Finishing connection"
        "READY" -> "Starting VPN"
        else -> return "Tap to cancel"
    }
    return "$label · Tap to cancel"
}
