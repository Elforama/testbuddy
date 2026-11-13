package dev.testbuddy.agent

/**
 * When each window was first seen (issue #204). Android drops a touch sent into a window just
 * added whose input channel is not up yet (InputDispatcher: "NO_INPUT_CHANNEL"), and the app sees
 * nothing of it. A tap is held until the window under it is old enough ([Ops]).
 *
 * Only additions count: a window's title, bounds or focus changing, or another window coming or
 * going, leave its age as it was. Windows already there when the agent started are old.
 */
class WindowAges {
    private val lock = Any()
    private val firstSeen = HashMap<Int, Long>() // window id -> uptime ms first seen (0: old)
    private var primed = false
    /** The latest first-seen time of any window still listed: nothing is younger than this. */
    @Volatile var newest = 0L; private set

    /** The windows on screen when the agent starts: old. */
    fun prime(ids: IntArray?) {
        if (ids == null) return
        synchronized(lock) {
            if (primed) return
            ids.forEach { firstSeen[it] = 0L }
            primed = true
        }
    }

    /**
     * The windows listed after a change, at [at] (the event's time, uptime ms). Ids not seen
     * before are new from [at]; ids no longer listed are forgotten. Before [prime], all are old.
     */
    fun update(ids: IntArray?, at: Long) {
        if (ids == null) return
        synchronized(lock) {
            val fresh = primed
            primed = true
            val keep = ids.toHashSet()
            firstSeen.keys.retainAll(keep)
            for (id in ids) if (id !in firstSeen) firstSeen[id] = if (fresh) at else 0L
            newest = firstSeen.values.maxOrNull() ?: 0L
        }
    }

    /** When [id] was first seen (0: old, or not known). */
    fun firstSeen(id: Int): Long = synchronized(lock) { firstSeen[id] ?: 0L }
}
