package dev.testbuddy.agent

/** Optional diagnostics for one complete snapshot; no clocks are read when disabled. */
class ReadProfile(val nanoTime: () -> Long = System::nanoTime) {
    enum class Part { CACHE, WINDOWS, ROOTS, CHILDREN }
    private val started = nanoTime()
    private val nanos = LongArray(Part.entries.size)
    private val calls = IntArray(Part.entries.size)
    var nodes = 0

    fun record(part: Part, elapsed: Long) {
        nanos[part.ordinal] += elapsed
        calls[part.ordinal]++
    }

    fun write(j: Json) {
        val total = nanoTime() - started
        j.key("profile").obj().put("totalMs", total / 1e6)
        for (part in Part.entries) {
            val name = part.name.lowercase()
            j.put(name + "Ms", nanos[part.ordinal] / 1e6)
                .put(name + "Calls", calls[part.ordinal])
        }
        // Remaining time includes cached node properties, traversal, and JSON writing.
        j.put("walkEncodeMs", maxOf(0L, total - nanos.sum()) / 1e6).put("nodes", nodes).endObj()
    }
}

internal inline fun <T> ReadProfile?.measure(part: ReadProfile.Part, block: () -> T): T {
    if (this == null) return block()
    val start = nanoTime()
    try { return block() } finally { record(part, nanoTime() - start) }
}
