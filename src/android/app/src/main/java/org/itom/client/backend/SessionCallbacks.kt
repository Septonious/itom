package org.itom.client.backend

import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import java.util.concurrent.locks.ReentrantReadWriteLock
import kotlin.concurrent.read
import kotlin.concurrent.write

// Connectivity callbacks run on a framework thread, outside runSession's try
// block. An ordinary platform failure must terminate this generation through
// its normal cleanup, rather than escape that thread and kill the process.
internal class SessionCallbacks(
    private val active: () -> Boolean,
    private val onFailure: () -> Unit,
) {
    private val closed = AtomicBoolean(false)
    private val firstFailure = AtomicReference<String?>(null)
    private val lifetime = ReentrantReadWriteLock()
    val failure: String? get() = firstFailure.get()

    fun <T> call(code: String, fallback: T, operation: () -> T): T = lifetime.read {
        if (closed.get() || firstFailure.get() != null || !active()) return@read fallback
        try { operation() } catch (_: Exception) {
            if (firstFailure.compareAndSet(null, code)) onFailure()
            fallback
        }
    }

    // Wait for callbacks already using the native trace client before its
    // teardown. Late queued callbacks see closed and cannot use that client.
    fun close() { lifetime.write { closed.set(true) } }
}
