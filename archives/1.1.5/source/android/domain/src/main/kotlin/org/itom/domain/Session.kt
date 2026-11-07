package org.itom.domain

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.Locale

enum class Phase { OFF, CONNECTING, CONNECTED, ERROR }
data class Metrics(val downBps: Long = 0, val upBps: Long = 0, val rttMs: Int? = null, val tcp: Int = 0, val activeLines: Int = 0, val totalLines: Int = 0)
data class Sample(val downBps: Long, val upBps: Long)
data class SessionSnapshot(
    val phase: Phase = Phase.OFF, val generation: Long = 0, val configRevision: Long = 0,
    val profileId: String? = null, val startedAt: Long? = null, val metrics: Metrics = Metrics(),
    val history: List<Sample> = emptyList(), val error: String? = null,
    val vpnInterfaceUp: Boolean = false,
    val transport: String = "{}", val sampledAt: Long? = null,
)

/** Synchronous generation checks make cancellation linearizable across coroutine callbacks. */
class SessionMachine {
    private val mutable = MutableStateFlow(SessionSnapshot())
    val state: StateFlow<SessionSnapshot> = mutable.asStateFlow()
    @Synchronized fun begin(profileId: String, revision: Long, total: Int): Long {
        val next = mutable.value.generation + 1
        mutable.value = SessionSnapshot(Phase.CONNECTING, next, revision, profileId, metrics = Metrics(totalLines = total))
        return next
    }
    /** Unavailable transport rejects a start without emitting a fictitious connecting state. */
    @Synchronized fun reject(profileId: String, revision: Long, total: Int, code: String) {
        mutable.value = SessionSnapshot(phase = Phase.ERROR, generation = mutable.value.generation + 1,
            configRevision = revision, profileId = profileId, metrics = Metrics(totalLines = total), error = code)
    }
    @Synchronized fun connected(generation: Long, now: Long, interfaceUp: Boolean = false): Boolean {
        val s = mutable.value
        if (s.generation != generation || s.phase != Phase.CONNECTING) return false
        mutable.value = s.copy(phase = Phase.CONNECTED, startedAt = now, vpnInterfaceUp = interfaceUp)
        return true
    }
    @Synchronized fun metrics(generation: Long, metrics: Metrics): Boolean {
        if (mutable.value.phase != Phase.CONNECTED) return false
        return sample(generation, metrics, mutable.value.transport, mutable.value.sampledAt)
    }

    @Synchronized fun sample(generation: Long, metrics: Metrics, transport: String, sampledAt: Long?): Boolean {
        val s = mutable.value
        if (s.generation != generation || s.phase !in setOf(Phase.CONNECTING, Phase.CONNECTED)) return false
        demand(metrics.downBps >= 0 && metrics.upBps >= 0 && metrics.tcp >= 0 && metrics.activeLines in 0..metrics.totalLines && (metrics.rttMs == null || metrics.rttMs >= 0), "INVALID_METRICS")
        mutable.value = s.copy(metrics = metrics, transport = transport, sampledAt = sampledAt, history = if (s.phase == Phase.CONNECTED) (s.history + Sample(metrics.downBps, metrics.upBps)).takeLast(60) else s.history)
        return true
    }
    @Synchronized fun fail(generation: Long, code: String): Boolean {
        val s = mutable.value
        if (s.generation != generation || s.phase == Phase.OFF || s.phase == Phase.ERROR) return false
        // A real host must close its TUN before publishing this terminal event.
        mutable.value = s.copy(phase = Phase.ERROR, metrics = Metrics(totalLines = s.metrics.totalLines), error = code, vpnInterfaceUp = false, transport = "{}", sampledAt = null)
        return true
    }
    @Synchronized fun stop(expectedGeneration: Long? = null): Boolean {
        // Delayed notification commands belong to the session that created them.
        if (expectedGeneration != null && mutable.value.generation != expectedGeneration) return false
        mutable.value = SessionSnapshot(generation = mutable.value.generation + 1)
        return true
    }
}

object Presentation {
    fun rate(bitsPerSecond: Long): String = if (bitsPerSecond >= 1_000_000)
        String.format(Locale.ROOT, "%.1f Mbps", bitsPerSecond / 1_000_000.0)
    else String.format(Locale.ROOT, "%.0f kbps", bitsPerSecond.coerceAtLeast(0) / 1000.0)
    fun rtt(ms: Int?): String = ms?.let { "$it ms" } ?: "—"
    fun rttBand(ms: Int?, thresholds: RttThresholds = RttThresholds()): Int = when { ms == null -> 0; ms <= thresholds.greenMax -> 1; ms <= thresholds.yellowMax -> 2; else -> 3 }
    fun orbit(angle: Float, elapsedNanos: Long, scale: Float = 1f): Float = if (scale <= 0f) angle else ((angle + elapsedNanos.coerceIn(0, 100_000_000) / 1_000_000_000f * 145f / scale) % 360f)
    fun notification(s: SessionSnapshot): String = "↓ ${rate(s.metrics.downBps)}   ↑ ${rate(s.metrics.upBps)}   RTT ${rtt(s.metrics.rttMs)}"
}
