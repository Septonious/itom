package org.itom.domain

import java.security.SecureRandom
import java.util.Base64

/** Generate only missing fields; existing credentials remain stable across toggles. */
fun SocksDraft.withAuthentication(enabled: Boolean): SocksDraft {
    if (!enabled || username.isNotBlank() && password.isNotEmpty()) return copy(authentication = enabled)
    val random = SecureRandom()
    fun credential(bytes: Int): String = ByteArray(bytes).also(random::nextBytes).let {
        Base64.getUrlEncoder().withoutPadding().encodeToString(it)
    }
    return copy(authentication = true,
        username = if (username.isBlank()) "itom_" + credential(9) else username,
        password = if (password.isEmpty()) credential(24) else password)
}
