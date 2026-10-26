package org.itom.domain

/** Only application-defined codes may cross into the UI or diagnostics. */
fun nativeFailureCode(message: String?): String = message?.takeIf {
    it in setOf("SOCKS_PORT_IN_USE", "UNDERLYING_DNS_MISSING", "UNDERLYING_DNS_INVALID",
        "NATIVE_CONFIG", "DNS_ADDRESS", "DNS_BACKUP_REQUIRED", "DNS_DUPLICATE",
        "V2_STARTUP_PROFILE", "V2_STARTUP_WALLET", "V2_STARTUP_IMAP",
        "V2_STARTUP_RENDEZVOUS", "V2_STARTUP_NOISE", "V2_STARTUP_ROSTER",
        "V2_STARTUP_RUNTIME", "V2_STARTUP_PERMISSION")
} ?: "TRANSPORT_FAILED"
