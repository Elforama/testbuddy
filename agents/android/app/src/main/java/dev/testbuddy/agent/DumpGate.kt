package dev.testbuddy.agent

import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

internal class DumpGate(private val execute: (Runnable) -> Unit) {
    private val inFlight = ConcurrentHashMap<String, AtomicBoolean>()

    fun run(service: String, dump: () -> Unit, closeWrite: () -> Unit, read: () -> Pair<ByteArray, Boolean>): ShellResult? {
        val busy = inFlight.getOrPut(service) { AtomicBoolean() }
        if (!busy.compareAndSet(false, true)) {
            closeWrite()
            return ShellResult(-1, "", true)
        }
        val failed = AtomicReference<Throwable?>()
        try {
            execute(Runnable {
                try { dump() } catch (t: Throwable) { failed.set(t) }
                finally {
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
        if (!timedOut && failed.get() != null) return null
        return ShellResult(if (timedOut) -1 else 0, String(raw, Charsets.UTF_8), timedOut, raw)
    }
}
