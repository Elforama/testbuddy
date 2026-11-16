package dev.testbuddy.agent

/**
 * When each window was first seen (issue #204). Android drops a touch sent into a window just
 * added whose input channel is not up yet (InputDispatcher: "NO_INPUT_CHANNEL"), and the app sees
 * nothing of it. A tap is held until the window under it is old enough ([Ops]).
 *
 * Only additions count: a window's title, bounds or focus changing, or another window coming or
 * going, leave its age as it was. Windows already there when the agent started are old.
 *
 * Also which windows took input (issue #280): a touch or key that is the first a window just
 * added ever got may be swallowed while the window comes in (a dialog's guard against early
 * taps, an enter transition), and the host may send such a tap again if it provably did
 * nothing. A window that already took input never counts as new again, the app's own window
 * back after a modal closed included. A full-screen dialog drops the app's window from the
 * list while it is up, and it is listed again, with the same id, when the dialog closes: so
 * this record ([history]) outlives the list, unlike the ages above (seen on an emulator: the
 * app's window back after a dialog closed was taken for one 257 ms old). Android gives every
 * new window a new id, so an id seen again is the same window. Ids only grow: one no higher
 * than the highest listed when the agent started belongs to a window that was there then, even
 * if it was hidden (under a full-screen dialog) and is listed only later: old, never "first".
 */
class WindowAges {
    private val lock = Any()
    private val firstSeen = HashMap<Int, Long>() // window id -> uptime ms first seen (0: old)
    /** Every window id seen (the latest [HISTORY] of them): when it first came, whether it took input. */
    private val history = object : LinkedHashMap<Int, Seen>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Int, Seen>) = size > HISTORY
    }
    // at: uptime ms first seen (0: old); before: the ids listed just before it came.
    private class Seen(val at: Long, var tookInput: Boolean = false, val before: IntArray = IntArray(0))
    /** What opened each window (its window-state event): its package and class. */
    class Opened(val pkg: String?, val cls: String?)
    private val openedBy = object : LinkedHashMap<Int, Opened>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Int, Opened>) = size > HISTORY
    }
    private var primed = false
    private var primedMax = Int.MIN_VALUE // the highest id listed when the agent started
    /** The latest first-seen time of any window still listed: nothing is younger than this. */
    @Volatile var newest = 0L; private set

    /** The windows on screen when the agent starts: old. */
    fun prime(ids: IntArray?) {
        if (ids == null) return
        synchronized(lock) {
            if (primed) return
            ids.forEach { firstSeen[it] = 0L; history.getOrPut(it) { Seen(0L) } }
            primedMax = ids.maxOrNull() ?: Int.MIN_VALUE
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
            val before = firstSeen.keys.toIntArray()
            firstSeen.keys.retainAll(keep)
            for (id in ids) if (id !in firstSeen) {
                firstSeen[id] = if (fresh && id > primedMax) at else 0L
                history.getOrPut(id) { Seen(firstSeen[id]!!, before = before.filter { it != id }.toIntArray()) }
            }
            newest = firstSeen.values.maxOrNull() ?: 0L
        }
    }

    /** When [id] was first seen (0: old, or not known). */
    fun firstSeen(id: Int): Long = synchronized(lock) { firstSeen[id] ?: 0L }

    /** [id]'s window-state event (the first one): what opened it. */
    fun opened(id: Int, pkg: String?, cls: String?) = synchronized(lock) {
        if (id !in openedBy) openedBy[id] = Opened(pkg, cls)
    }

    /** What opened [id], null when its window-state event was not seen. */
    fun openedBy(id: Int): Opened? = synchronized(lock) { openedBy[id] }

    /** The ids listed just before [id] came (empty when not known). */
    fun listedBefore(id: Int): List<Int> = synchronized(lock) { history[id]?.before?.toList() ?: emptyList() }

    /** When [id] first came, ever (0: old, or not known): its time in the list may be shorter. */
    fun firstAppeared(id: Int): Long = synchronized(lock) { history[id]?.at ?: 0L }

    /**
     * [id] took a touch or key: true when it is the first input it ever got. Only a window seen
     * being added can say so: one already there when the agent started, or never listed, never
     * does (whether it took input before is not known).
     */
    fun tookInput(id: Int): Boolean = synchronized(lock) {
        val seen = history[id] ?: return false
        val first = !seen.tookInput && seen.at > 0
        seen.tookInput = true
        first
    }

    private companion object {
        /** Window ids remembered: far more than an app opens in a run's flows between restarts. */
        const val HISTORY = 4096
    }
}
