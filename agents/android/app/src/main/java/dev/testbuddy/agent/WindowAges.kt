package dev.testbuddy.agent

class WindowAges {
    private val lock = Any()
    private val firstSeen = HashMap<Int, Long>()
    private val history = object : LinkedHashMap<Int, Seen>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Int, Seen>) = size > HISTORY
    }
    private class Seen(val at: Long, var tookInput: Boolean = false, val before: IntArray = IntArray(0), var box: Box? = null)
    class Opened(val pkg: String?, val cls: String?, val at: Long)
    private val openedBy = object : LinkedHashMap<Int, MutableList<Opened>>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Int, MutableList<Opened>>) = size > HISTORY
    }
    private var primed = false
    private var primedMax = Int.MIN_VALUE
    @Volatile var newest = 0L; private set

    fun prime(ids: IntArray?, boxes: Map<Int, Box>? = null) {
        if (ids == null) return
        synchronized(lock) {
            if (primed) return
            ids.forEach { firstSeen[it] = 0L; history.getOrPut(it) { Seen(0L) } }
            keepBoxes(boxes)
            primedMax = ids.maxOrNull() ?: Int.MIN_VALUE
            primed = true
        }
    }

    fun update(ids: IntArray?, at: Long, boxes: Map<Int, Box>? = null) {
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
            keepBoxes(boxes)
            newest = firstSeen.values.maxOrNull() ?: 0L
        }
    }

    private fun keepBoxes(boxes: Map<Int, Box>?) {
        boxes?.forEach { (id, box) -> if (!box.isEmpty) history[id]?.box = box }
    }

    fun boundsOf(id: Int): Box? = synchronized(lock) { history[id]?.box }

    fun listed(id: Int): Boolean = synchronized(lock) { firstSeen.containsKey(id) }

    fun firstSeen(id: Int): Long = synchronized(lock) { firstSeen[id] ?: 0L }

    fun opened(id: Int, pkg: String?, cls: String?, at: Long) = synchronized(lock) {
        val list = openedBy.getOrPut(id) { ArrayList() }
        if (list.size < OPENED_PER_WINDOW) list += Opened(pkg, cls, at)
    }

    fun openedBy(id: Int): List<Opened> = synchronized(lock) { openedBy[id]?.toList() ?: emptyList() }

    fun listedBefore(id: Int): List<Int> = synchronized(lock) { history[id]?.before?.toList() ?: emptyList() }

    fun firstAppeared(id: Int): Long = synchronized(lock) { history[id]?.at ?: 0L }

    fun tookInput(id: Int): Boolean = synchronized(lock) {
        val seen = history[id] ?: return false
        val first = !seen.tookInput && seen.at > 0
        seen.tookInput = true
        first
    }

    private companion object {
        const val HISTORY = 4096
        const val OPENED_PER_WINDOW = 8
    }
}
