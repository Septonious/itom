package org.itom.domain

data class NetworkCandidate(val handle: Long, val kind: NetworkKind, val validated: Boolean)

/** Candidates are physical internet networks with DNS; VPN itself is excluded by the adapter. */
fun selectUnderlyingNetwork(candidates: List<NetworkCandidate>, preferred: Long?, previous: Long?): NetworkCandidate? {
    val active = candidates.firstOrNull { it.handle == preferred }
    val retained = candidates.firstOrNull { it.handle == previous }
    val sorted = candidates.sortedWith(compareByDescending<NetworkCandidate> { it.validated }
        .thenByDescending { it.kind == NetworkKind.WIFI }.thenBy { it.handle })
    return active?.takeIf { it.validated } ?: sorted.firstOrNull { it.validated }
        ?: active ?: retained ?: sorted.firstOrNull()
}
