package org.itom.client.backend

import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*
import org.junit.Test

class SessionCallbacksTest {
    @Test fun frameworkFailureRequestsSessionCleanupInsteadOfEscapingCallbackThread() {
        val cleanup = AtomicInteger()
        val callbacks = SessionCallbacks({ true }, { cleanup.incrementAndGet() })
        assertNull(callbacks.call("UNDERLYING_NETWORK_FAILED", null as String?) { throw SecurityException("binder refused network") })
        assertEquals("UNDERLYING_NETWORK_FAILED", callbacks.failure)
        assertEquals(1, cleanup.get())
        assertEquals("[]", callbacks.call("DNS_FAILED", "[]") { error("callback ran after failure") })
        assertEquals(1, cleanup.get())
    }

    @Test fun concurrentNetworkLossReportsOneFailureAndDoesNotEscapeNativeCallbacks() {
        val cleanup = AtomicInteger()
        val entered = CountDownLatch(8)
        val release = CountDownLatch(1)
        val callbacks = SessionCallbacks({ true }, { cleanup.incrementAndGet() })
        val executor = java.util.concurrent.Executors.newFixedThreadPool(8)
        try {
            val workers = (1..8).map {
                executor.submit {
                    assertFalse(callbacks.call("UNDERLYING_NETWORK_FAILED", false) {
                        entered.countDown(); release.await()
                        throw IllegalArgumentException("network no longer exists")
                    })
                }
            }
            assertTrue(entered.await(5, java.util.concurrent.TimeUnit.SECONDS))
            release.countDown()
            workers.forEach { it.get(5, java.util.concurrent.TimeUnit.SECONDS) }
            assertEquals(1, cleanup.get())
        } finally { release.countDown(); executor.shutdownNow() }
    }

    @Test fun closedAndReplacedSessionsCannotApplyLateNetworkCallbacks() {
        var generation = 1
        var mutations = 0
        val callbacks = SessionCallbacks({ generation == 1 }, { error("stale callback requested cleanup") })
        callbacks.call("NETWORK_FAILED", Unit) { mutations++ }
        generation = 2
        callbacks.call("NETWORK_FAILED", Unit) { mutations++ }
        generation = 1
        callbacks.close()
        callbacks.call("NETWORK_FAILED", Unit) { mutations++ }
        assertEquals(1, mutations)
        assertNull(callbacks.failure)
    }

    @Test fun nativeTeardownWaitsForCallbackAlreadyInProgress() {
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val executor = java.util.concurrent.Executors.newFixedThreadPool(2)
        val callbacks = SessionCallbacks({ true }, { error("unexpected failure") })
        try {
            val worker = executor.submit { callbacks.call("NETWORK_FAILED", Unit) { entered.countDown(); release.await() } }
            assertTrue(entered.await(5, java.util.concurrent.TimeUnit.SECONDS))
            val closeStarted = CountDownLatch(1)
            val close = executor.submit { closeStarted.countDown(); callbacks.close() }
            assertTrue(closeStarted.await(5, java.util.concurrent.TimeUnit.SECONDS))
            assertFailsWith<java.util.concurrent.TimeoutException> { close.get(100, java.util.concurrent.TimeUnit.MILLISECONDS) }
            release.countDown()
            worker.get(5, java.util.concurrent.TimeUnit.SECONDS)
            close.get(5, java.util.concurrent.TimeUnit.SECONDS)
            callbacks.call("NETWORK_FAILED", Unit) { error("native callback ran after close") }
        } finally { release.countDown(); executor.shutdownNow() }
    }
}
