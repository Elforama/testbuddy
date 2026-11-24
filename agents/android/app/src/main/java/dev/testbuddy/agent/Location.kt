package dev.testbuddy.agent

import java.math.BigDecimal

class Location(private val device: Device, private val clock: Clock, private val background: (Runnable) -> Unit) {
    private val shellLock = Object()
    private val lock = Object()
    private var providers: List<String> = emptyList()
    private var fix: String? = null
    private var owner: Any? = null
    private var everyMs = DEFAULT_EVERY_MS
    private var generation = 0
    private var tick: Runnable? = null

    val isOn: Boolean get() = synchronized(lock) { fix != null }

    fun set(lat: Double, lon: Double, every: Long, client: Any?): List<String> = synchronized(shellLock) {
        var on = synchronized(lock) { providers }
        val added = on.isEmpty()
        if (added) {
            device.shell("echo ${PROVIDERS.joinToString(" ")} > $LIST", SHELL_TIMEOUT_MS)
            on = addProviders()
            if (on.isEmpty()) {
                remove(PROVIDERS)
                throw IllegalStateException("could not add test location providers on the device")
            }
            synchronized(lock) { providers = on }
        }
        val at = plain(lat) + "," + plain(lon)
        val cmd = on.joinToString("; ") { "cmd location providers set-test-provider-location $it --location $at --accuracy 1" }
        val res = device.shell(cmd, SHELL_TIMEOUT_MS)
        if (res.code != 0 || res.out.contains("Exception")) {
            if (added) {
                synchronized(lock) { providers = emptyList() }
                remove(on)
            }
            throw IllegalStateException("setting the location: ${res.out.trim()}")
        }
        synchronized(lock) {
            fix = cmd
            owner = client
            everyMs = every.coerceIn(100, 60_000)
            schedule(++generation)
        }
        on
    }

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
        remove(gone)
    }

    fun released(client: Any) {
        synchronized(lock) {
            if (owner !== client || fix == null) return
            clock.postDelayed(Runnable { background(Runnable { off(onlyFor = client) }) }, GRACE_MS)
        }
    }

    private fun remove(ps: List<String>) {
        if (ps.isNotEmpty()) {
            device.shell(ps.joinToString("; ") { "cmd location providers remove-test-provider $it" } + "; rm -f $LIST", SHELL_TIMEOUT_MS)
        }
    }

    private fun schedule(g: Int) {
        tick?.let { clock.cancel(it) }
        val t = Runnable { background(Runnable { repeat(g) }) }
        tick = t
        clock.postDelayed(t, everyMs)
    }

    private fun repeat(g: Int) {
        synchronized(shellLock) {
            val cmd = synchronized(lock) { if (g != generation) null else fix } ?: return
            try { device.shell(cmd, SHELL_TIMEOUT_MS) } catch (_: Throwable) {}
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
        const val GRACE_MS = 50_000L
        private const val SHELL_TIMEOUT_MS = 5_000
        private val PROVIDERS = listOf("gps", "network", "fused")
        const val LIST = "/data/local/tmp/tb-location-providers"

        fun plain(v: Double): String = BigDecimal.valueOf(v).toPlainString()
    }
}
