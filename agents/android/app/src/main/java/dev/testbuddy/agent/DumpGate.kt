package dev.testbuddy.agent

import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

/**
 * The bookkeeping of BinderDump's in-process dumps (issue #99 review), apart from the pipe so
 * it runs on the JVM in tests:
 * - one dump in flight per service: while one is stuck (its thread still blocked in the
 *   service), the next is not started, and gets the timed-out answer at once. Otherwise each
 *   Enter on a stuck service would leave one more blocked thread and pipe end in the agent.
 * - a dump call that throws (no permission, the service died) is not taken for an empty dump:
 *   the answer is null, and the caller runs `dumpsys` in the shell instead.
 */
internal class DumpGate(private val execute: (Runnable) -> Unit) {
    private val inFlight = ConcurrentHashMap<String, AtomicBoolean>()

    /**
     * Runs [dump] (the binder call writing into the pipe) on [execute], then [closeWrite];
     * meanwhile [read] reads the pipe until the end or its timeout: (the bytes, timed out).
     * Null when the dump could not be made (the caller falls back to `dumpsys`).
     */
    fun run(service: String, dump: () -> Unit, closeWrite: () -> Unit, read: () -> Pair<ByteArray, Boolean>): ShellResult? {
        val busy = inFlight.getOrPut(service) { AtomicBoolean() }
        if (!busy.compareAndSet(false, true)) {
            closeWrite()
            return ShellResult(-1, "", true) // the last one is still stuck: so is the service
        }
        val failed = AtomicReference<Throwable?>()
        try {
            execute(Runnable {
                try { dump() } catch (t: Throwable) { failed.set(t) }
                finally {
                    // Free before the write end closes: the reader sees the end only after it,
                    // so the next Enter never finds this finished dump still in flight.
                    busy.set(false)
                    closeWrite()
                }
            })
        } catch (t: Throwable) {
            busy.set(false)
            closeWrite()
            return null
        }
        val (raw, timedOut) = try { read() } catch (t: Throwable) { return null }
        // The output ended (the write end closed after the call): a call that threw made it.
        if (!timedOut && failed.get() != null) return null
        return ShellResult(if (timedOut) -1 else 0, String(raw, Charsets.UTF_8), timedOut, raw)
    }
}
