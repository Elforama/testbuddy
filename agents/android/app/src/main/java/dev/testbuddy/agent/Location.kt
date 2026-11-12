package dev.testbuddy.agent

import java.math.BigDecimal

/**
 * A mock location on the shell's test providers (gps, network and fused), which apps read
 * through the location service whatever provider they ask for (Android 12+). The fix is sent
 * again every [everyMs]: a watch that asks for updates at an interval gets them, and the fused
 * provider forgets a fix that does not come again.
 *
 * The host sends one "location" request per point (setLocation, each step of a travel), and
 * the repeats are the agent's own (issue #72): they used to be host requests every second,
 * which taps and screen reads queued behind (the agent answers one request at a time per
 * connection) and which took slots in the request-token cache. The repeats run on [background],
 * never on a connection's request thread, and a lock keeps them in order with the requests: a
 * repeat always carries the latest point.
 *
 * The repeats are a thread in the agent's process, not a shell loop on the device: a loop of
 * its own would outlive an agent that is killed or replaced, with nothing left to stop it.
 * They end, and the providers are removed (the device reports its own location again), when
 * the host turns the location off, when the connection that set the point closes (tb ended,
 * was killed, or lost its link: a driver that reconnects sets the point again on its new
 * connection), and with the agent.
 */
class Location(private val device: Device, private val clock: Clock, private val background: (Runnable) -> Unit) {
    /** Held while a shell command runs: a request's and a repeat's commands never interleave. */
    private val shellLock = Object()
    /** Guards the fields below. Taken inside [shellLock], never the other way round. */
    private val lock = Object()
    private var providers: List<String> = emptyList()
    private var fix: String? = null // the command that sends the current point
    private var owner: Any? = null // the connection that set it
    private var everyMs = DEFAULT_EVERY_MS
    private var generation = 0 // a new point or off: the repeats of the old one stop
    private var tick: Runnable? = null

    /** Sets the point now and repeats it every [every] ms. Returns the providers it is on. */
    fun set(lat: Double, lon: Double, every: Long, client: Any?): List<String> = synchronized(shellLock) {
        var on = synchronized(lock) { providers }
        if (on.isEmpty()) {
            on = addProviders()
            check(on.isNotEmpty()) { "could not add test location providers on the device" }
            synchronized(lock) { providers = on }
        }
        val at = plain(lat) + "," + plain(lon)
        val cmd = on.joinToString("; ") { "cmd location providers set-test-provider-location $it --location $at --accuracy 1" }
        val res = device.shell(cmd, SHELL_TIMEOUT_MS)
        check(res.code == 0 && !res.out.contains("Exception")) { "setting the location: ${res.out.trim()}" }
        synchronized(lock) {
            fix = cmd
            owner = client
            everyMs = every.coerceIn(100, 60_000)
            schedule(++generation)
        }
        on
    }

    /** Stops the repeats and removes the providers. With [onlyFor], only if that connection set the point. */
    fun off(onlyFor: Any? = null) = synchronized(shellLock) {
        val gone = synchronized(lock) {
            if (onlyFor != null && (owner !== onlyFor || fix == null)) return@synchronized emptyList()
            generation++
            tick?.let { clock.cancel(it) }
            tick = null
            fix = null
            owner = null
            providers.also { providers = emptyList() }
        }
        if (gone.isNotEmpty()) {
            device.shell(gone.joinToString("; ") { "cmd location providers remove-test-provider $it" }, SHELL_TIMEOUT_MS)
        }
    }

    /** The next repeat of generation [g], [everyMs] from now. [lock] is held. */
    private fun schedule(g: Int) {
        tick?.let { clock.cancel(it) }
        val t = Runnable { background(Runnable { repeat(g) }) }
        tick = t
        clock.postDelayed(t, everyMs)
    }

    private fun repeat(g: Int) {
        synchronized(shellLock) {
            val cmd = synchronized(lock) { if (g != generation) null else fix } ?: return
            try { device.shell(cmd, SHELL_TIMEOUT_MS) } catch (_: Throwable) {} // the next one tries again
            synchronized(lock) { if (g == generation) schedule(g) }
        }
    }

    private fun addProviders(): List<String> {
        device.shell("appops set com.android.shell android:mock_location allow", SHELL_TIMEOUT_MS)
        return PROVIDERS.filter { p ->
            device.shell("cmd location providers add-test-provider $p", SHELL_TIMEOUT_MS)
            val res = device.shell("cmd location providers set-test-provider-enabled $p true", SHELL_TIMEOUT_MS)
            res.code == 0 && !res.timedOut && !res.out.contains("Exception")
        }
    }

    companion object {
        const val DEFAULT_EVERY_MS = 1_000L
        private const val SHELL_TIMEOUT_MS = 5_000
        private val PROVIDERS = listOf("gps", "network", "fused")

        /** A coordinate as plain digits (Double.toString writes 1.0E-5). */
        fun plain(v: Double): String = BigDecimal.valueOf(v).toPlainString()
    }
}
