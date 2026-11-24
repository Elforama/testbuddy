package dev.testbuddy.agent

import android.os.SystemClock
import android.view.accessibility.AccessibilityWindowInfo
import java.util.Random
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

class Tree(
    private val nodes: Nodes,
    private val refreshBudgetMs: Long = REFRESH_BUDGET_MS,
    private val now: () -> Long = { SystemClock.uptimeMillis() },
) {
    private var virtualNodesSeen = false

    object Flag {
        const val CLICKABLE = 1
        const val ENABLED = 2
        const val FOCUSED = 4
        const val SCROLLABLE = 8
        const val CHECKED = 16
        const val SELECTED = 32
        const val CHECKABLE = 64
        const val PASSWORD = 128
        const val EDITABLE = 256
        const val LONG_CLICKABLE = 512
        const val FOCUSABLE = 1024
        const val SHOWING_HINT = 2048
        const val VISIBLE = 4096
        const val MORE_AFTER = 8192
        const val MORE_BEFORE = 16384
    }

    private fun freshNodes(fresh: Boolean, profile: ReadProfile?) {
        if (!fresh && !virtualNodesSeen) return
        try { profile.measure(ReadProfile.Part.CACHE) { nodes.clearCache() } } catch (_: Throwable) {}
    }

    fun write(j: Json, allWindows: Boolean, fresh: Boolean = false, profile: ReadProfile? = null,
              prefetchChildren: Boolean = true, includeHidden: Boolean = false): Boolean {
        freshNodes(fresh, profile)
        var virtual = false
        val read = nextRead.incrementAndGet()
        val written = HashMap<Int, Written>()
        j.key("windows").arr()
        val readAt = now()
        var windows: List<UiWindow> = readWindows(prefetchChildren, profile)
        heldBack = false
        var wrote = 0
        val activeRoot = if (windows.isEmpty()) activeRoot(prefetchChildren, profile) else null
        val activeSeen = try { activeRoot?.let { Triple(it.windowId, it.packageName, it.bounds) } } catch (_: Throwable) { null }
        if (activeRoot != null && activeSeen != null) {
            val (id, pkg, bounds) = activeSeen
            j.obj().put("id", id).put("type", 1).put("active", true).put("pkg", pkg)
            j.key("nodes").arr()
            virtual = walk(j, activeRoot, bounds, profile, includeHidden, written.getOrPut(id) { Written() }) or virtual
            j.endArr().endObj()
            wrote++
        }
        var listed = roots(windows, allWindows, profile)
        var fix: WindowFix? = null
        var cleared = false
        if (activeSeen == null) {
            val r = try { recoverOpened(listed, allWindows, prefetchChildren, profile) } catch (_: Throwable) { null }
            if (r != null) {
                listed = r.first
                fix = r.second
                windows = listed.map { it.first }
                cleared = true
            }
        }
        val unlisted = unlistedSince
        unlistedSince = null
        if (activeSeen == null && appUnread(listed)) {
            val r = try { recoverApp(listed, allWindows, prefetchChildren, profile, unlisted, cleared) } catch (_: Throwable) { null }
            if (r != null) {
                listed = r.first
                fix = r.second
                windows = listed.map { it.first }
            }
        }
        writeToast(j, windows)
        for ((w, root) in listed) {
            if (root == null) continue
            val pkg = try { root.packageName } catch (_: Throwable) { continue }
            val b = w.bounds
            j.obj().put("id", w.id).put("type", w.type).put("layer", w.layer)
                .put("active", w.active).put("focused", w.focused)
                .put("pkg", pkg)
            if (w.title != null) j.put("title", w.title)
            j.key("b").arr().num(b.left).num(b.top).num(b.right).num(b.bottom).endArr()
            j.key("nodes").arr()
            virtual = walk(j, root, b, profile, includeHidden, written.getOrPut(w.id) { Written() }) or virtual
            j.endArr().endObj()
            wrote++
        }
        j.endArr()
        j.put("read", read)
        keep(read, written)
        val nullRoots = listed.count { it.second == null }
        val appLeftOut = listed.any { it.second == null && it.first.type == AccessibilityWindowInfo.TYPE_APPLICATION }
        val pending = heldBack || wrote == 0
        if (fix != null || appLeftOut || pending) {
            j.key("windowFix").obj().put("nullRoots", nullRoots).put("how", fix?.how ?: if (pending) "pending" else "none")
            if (fix != null) j.put("window", fix.window).put("staleNullRoots", fix.staleNullRoots)
            if (pending) j.put("pending", true)
            j.endObj()
        }
        virtualNodesSeen = virtual
        return sawKeyboard(windows.any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }, readAt)
    }

    private fun readWindows(prefetchChildren: Boolean, profile: ReadProfile?): List<UiWindow> = try {
        profile.measure(ReadProfile.Part.WINDOWS) { nodes.windows(prefetchChildren) }
            .mapNotNull { try { SeenWindow(it) } catch (_: Throwable) { null } }
    } catch (_: Throwable) { emptyList() }

    private class SeenWindow(private val w: UiWindow) : UiWindow {
        override val id = w.id
        override val type = w.type
        override val layer = w.layer
        override val active = w.active
        override val focused = w.focused
        override val title = w.title
        override val bounds = w.bounds
        override fun root(): UiNode? = w.root()
    }

    private var heldBack = false

    private fun roots(windows: List<UiWindow>, allWindows: Boolean, profile: ReadProfile?): List<Pair<UiWindow, UiNode?>> =
        windows.filter { allWindows || it.type != AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY }
            .map { w -> w to (try { profile.measure(ReadProfile.Part.ROOTS) { w.root() } } catch (_: Throwable) { null }) }

    private class WindowFix(val how: String, val window: Int, val staleNullRoots: Int)

    private var futile: Pair<Int, List<Int>>? = null

    private var unlistedSince: Pair<Int, Long>? = null

    private fun appUnread(listed: List<Pair<UiWindow, UiNode?>>): Boolean =
        listed.isNotEmpty() && listed.none { it.first.type == AccessibilityWindowInfo.TYPE_APPLICATION && it.second != null }

    private fun activeRoot(prefetchChildren: Boolean, profile: ReadProfile?): UiNode? =
        try { profile.measure(ReadProfile.Part.ROOTS) { nodes.activeRoot(prefetchChildren) } } catch (_: Throwable) { null }

    private fun recoverApp(listed: List<Pair<UiWindow, UiNode?>>, allWindows: Boolean, prefetchChildren: Boolean,
                           profile: ReadProfile?, unlisted: Pair<Int, Long>?, cleared: Boolean = false): Pair<List<Pair<UiWindow, UiNode?>>, WindowFix?> {
        val first = activeRoot(prefetchChildren, profile) ?: return listed to null
        if (readable(listed, first.windowId)) return listed to null
        val staleNullRoots = listed.count { it.second == null }
        var again = listed
        var active: UiNode = first
        val key = first.windowId to listed.map { it.first.id }
        if (key != futile) {
            if (!cleared) {
                try { profile.measure(ReadProfile.Part.CACHE) { nodes.clearCache() } } catch (_: Throwable) {}
                again = roots(readWindows(prefetchChildren, profile), allWindows, profile)
            }
            val app = again.firstOrNull { it.first.type == AccessibilityWindowInfo.TYPE_APPLICATION && it.second != null }
            if (app != null) {
                futile = null
                return again to WindowFix("fresh read", app.first.id, staleNullRoots)
            }
            active = activeRoot(prefetchChildren, profile) ?: return again to null
            futile = active.windowId to again.map { it.first.id }
        }
        val id = active.windowId
        if (readable(again, id)) return again to null
        val at = again.indexOfFirst { it.first.id == id }
        if (at >= 0) {
            return again.toMutableList().apply { this[at] = this[at].first to active } to
                WindowFix("active root", id, staleNullRoots)
        }
        if (covered(active.bounds, again.filter { it.second != null }.map { it.first.bounds })) return again to null
        val t = now()
        val since = if (unlisted?.first == id) unlisted.second else t
        unlistedSince = id to since
        if (t - since < UNLISTED_GRACE_MS) {
            heldBack = true
            return again to null
        }
        return again + (ActiveWindow(active) to active) to WindowFix("active root", id, staleNullRoots)
    }

    @Volatile var openedWindow: () -> OpenedWindow? = { null }

    private var staleFor = -1
    private var staleTries = 0
    private var staleTriedAt = 0L

    private val goneWindows = object : LinkedHashMap<Int, Boolean>(16, 0.75f, false) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Int, Boolean>) = size > GONE_MEMORY
    }

    private fun recoverOpened(listed: List<Pair<UiWindow, UiNode?>>, allWindows: Boolean, prefetchChildren: Boolean,
                              profile: ReadProfile?): Pair<List<Pair<UiWindow, UiNode?>>, WindowFix?>? {
        val opened = try { openedWindow() } catch (_: Throwable) { null } ?: return null
        val id = opened.id
        if (id < 0 || goneWindows.containsKey(id) || listed.any { it.first.id == id }) return null
        if (listed.isEmpty() || appUnread(listed)) return null
        val t = now()
        if (staleFor != id) {
            staleFor = id
            staleTries = 0
        } else if (t - staleTriedAt < STALE_RETRY_MS) return null
        staleTries++
        staleTriedAt = t
        try { profile.measure(ReadProfile.Part.CACHE) { nodes.clearCache() } } catch (_: Throwable) {}
        val again = roots(readWindows(prefetchChildren, profile), allWindows, profile)
        if (readable(again, id)) return again to WindowFix("stale list", id, listed.count { it.second == null })
        if (staleTries >= STALE_TRIES ||
            (t - opened.at >= STALE_RETRY_MS && activeRoot(prefetchChildren, profile)?.windowId != id)) goneWindows[id] = true
        return again to null
    }

    companion object {
        const val UNLISTED_GRACE_MS = 1500L
        const val STALE_RETRY_MS = 1000L
        const val STALE_TRIES = 3
        const val GONE_MEMORY = 16
        const val KEPT_READS = 8
        const val REFRESH_BUDGET_MS = 100L
        private const val NO_LIFT = Int.MIN_VALUE
    }

    private fun readable(l: List<Pair<UiWindow, UiNode?>>, id: Int) = l.any { it.first.id == id && it.second != null }

    private fun covered(b: Box, boxes: List<Box>): Boolean {
        if (b.isEmpty) return true
        var inside = 0
        for (i in 0 until 16) for (k in 0 until 16) {
            val x = b.left + (2 * i + 1) * b.width / 32
            val y = b.top + (2 * k + 1) * b.height / 32
            if (boxes.any { x >= it.left && x < it.right && y >= it.top && y < it.bottom }) inside++
        }
        return inside >= 16 * 16 * 9 / 10
    }

    private class ActiveWindow(root: UiNode) : UiWindow {
        override val id = root.windowId
        override val type = root.windowType.takeIf { it > 0 } ?: AccessibilityWindowInfo.TYPE_APPLICATION
        override val layer = 0
        override val active = true
        override val focused = true
        override val title: CharSequence? = null
        override val bounds = root.bounds
        override fun root(): UiNode? = null
    }

    private fun writeToast(j: Json, windows: List<UiWindow>) {
        val texts = Toasts.showing(now())
        if (texts.isEmpty()) return
        var screen = Box.EMPTY
        for (w in windows) screen = screen.union(w.bounds)
        if (screen.isEmpty) return
        val h = screen.height / 20
        val w = screen.width * 2 / 3
        val l = screen.centerX - w / 2
        var t = screen.bottom - screen.height / 8 - h * texts.size
        j.obj().put("id", -2).put("type", 3).put("pkg", "android").put("title", "Toast")
        j.key("b").arr().num(l).num(t).num(l + w).num(t + h * texts.size).endArr()
        j.key("nodes").arr()
        for (text in texts) {
            j.obj().put("d", 0)
            j.key("b").arr().num(l).num(t).num(l + w).num(t + h).endArr()
            j.put("c", "android.widget.Toast").put("t", text).put("f", Flag.ENABLED or Flag.VISIBLE)
            j.endObj()
            t += h
        }
        j.endArr().endObj()
    }

    private fun walk(j: Json, root: UiNode, area: Box, profile: ReadProfile?, includeHidden: Boolean,
                     sink: Written): Boolean {
        var virtual = false
        val nodes = ArrayDeque<UiNode>()
        val depths = ArrayDeque<Int>()
        val inWeb = ArrayDeque<Boolean>()
        val underHidden = ArrayDeque<Boolean>()
        val clips = ArrayDeque<Box>()
        val lifts = ArrayDeque<Int>()
        nodes.addLast(root); depths.addLast(0); inWeb.addLast(false); underHidden.addLast(false)
        clips.addLast(area); lifts.addLast(NO_LIFT)
        while (nodes.isNotEmpty()) {
            val n = nodes.removeLast()
            val depth = depths.removeLast()
            val inherited = inWeb.removeLast()
            val passed = underHidden.removeLast()
            val clip = clips.removeLast()
            val lift = lifts.removeLast()
            val shown = n === root || includeHidden || inherited || n.visibleToUser
            val bounds = n.bounds
            if (!shown || (passed && !overlaps(bounds, clip))) {
                if (!overlaps(bounds, clip)) continue
                val order = if (lift == NO_LIFT) n.drawingOrder else lift
                for (i in n.childCount - 1 downTo 0) {
                    val c = try { profile.measure(ReadProfile.Part.CHILDREN) { n.child(i) } } catch (_: Throwable) { null } ?: continue
                    nodes.addLast(c); depths.addLast(depth); inWeb.addLast(false); underHidden.addLast(true)
                    clips.addLast(clip); lifts.addLast(order)
                }
                continue
            }
            val cls = n.className?.toString()
            val web = inherited || (cls != null && cls.contains("WebView"))
            if (web || cls?.startsWith("androidx.compose.ui.platform.") == true) virtual = true
            val written = if (passed) cut(bounds, clip) else bounds
            writeNode(j, n, depth, cls, written, if (lift == NO_LIFT) n.drawingOrder else lift)
            sink.add(n)
            if (profile != null) profile.nodes++
            val inner = cut(written, clip)
            for (i in n.childCount - 1 downTo 0) {
                val c = try { profile.measure(ReadProfile.Part.CHILDREN) { n.child(i) } } catch (_: Throwable) { null } ?: continue
                nodes.addLast(c); depths.addLast(depth + 1); inWeb.addLast(web); underHidden.addLast(passed)
                clips.addLast(inner); lifts.addLast(NO_LIFT)
            }
        }
        return virtual
    }

    private fun overlaps(b: Box, clip: Box) =
        !b.isEmpty && b.left < clip.right && clip.left < b.right && b.top < clip.bottom && clip.top < b.bottom

    private fun cut(b: Box, clip: Box): Box {
        val c = Box(maxOf(b.left, clip.left), maxOf(b.top, clip.top), minOf(b.right, clip.right), minOf(b.bottom, clip.bottom))
        return if (c.isEmpty) Box.EMPTY else c
    }

    private fun writeNode(j: Json, n: UiNode, depth: Int, cls: String?, bounds: Box, order: Int) {
        j.obj().put("d", depth)
        j.key("b").arr().num(bounds.left).num(bounds.top).num(bounds.right).num(bounds.bottom).endArr()
        if (cls != null) j.put("c", cls)
        val text = n.text
        if (!text.isNullOrEmpty()) j.put("t", text)
        val cd = n.contentDescription
        if (!cd.isNullOrEmpty()) j.put("cd", cd)
        val id = n.viewId
        if (!id.isNullOrEmpty()) j.put("id", id)
        val hint = n.hint
        if (!hint.isNullOrEmpty()) j.put("h", hint)
        val err = n.error
        if (!err.isNullOrEmpty()) j.put("e", err)
        val sd = n.stateDescription
        if (!sd.isNullOrEmpty()) j.put("sd", sd)
        var f = 0
        if (n.clickable) f = f or Flag.CLICKABLE
        if (n.enabled) f = f or Flag.ENABLED
        if (n.focused) f = f or Flag.FOCUSED
        if (n.scrollable) f = f or Flag.SCROLLABLE
        if (n.checked) f = f or Flag.CHECKED
        if (n.selected) f = f or Flag.SELECTED
        if (n.checkable) f = f or Flag.CHECKABLE
        if (n.password) f = f or Flag.PASSWORD
        if (n.editable) f = f or Flag.EDITABLE
        if (n.longClickable) f = f or Flag.LONG_CLICKABLE
        if (n.focusable) f = f or Flag.FOCUSABLE
        if (n.showingHint) f = f or Flag.SHOWING_HINT
        if (n.visibleToUser) f = f or Flag.VISIBLE
        if (n.scrollable) {
            if (n.canScrollForward) f = f or Flag.MORE_AFTER
            if (n.canScrollBackward) f = f or Flag.MORE_BEFORE
        }
        if (f != 0) j.put("f", f)
        n.collection?.let { j.key("ci").arr().num(it[0]).num(it[1]).endArr() }
        n.collectionItem?.let { j.key("ii").arr().num(it[0]).num(it[1]).endArr() }
        if (order >= 0) j.put("o", order)
        j.endObj()
    }

    fun focusedInput(): Field? = try { nodes.focusedInput() } catch (_: Throwable) { null }

    class Written {
        var count = 0
            private set
        val kept = HashMap<Int, UiNode>()

        fun add(n: UiNode) {
            count++
            if (aimable(n)) kept[count] = n
        }

        private fun aimable(n: UiNode) = n.visibleToUser &&
            (n.clickable || n.longClickable || n.checkable || n.editable || n.focusable ||
                !n.text.isNullOrEmpty() || !n.contentDescription.isNullOrEmpty() ||
                !n.viewId.isNullOrEmpty() || !n.hint.isNullOrEmpty())
    }

    private val kept = ArrayDeque<Pair<Long, Map<Int, Written>>>()
    private val nextRead = AtomicLong((Random().nextInt(Int.MAX_VALUE).toLong() + 1) shl 20)

    private fun keep(read: Long, written: Map<Int, Written>) = synchronized(kept) {
        kept.addLast(read to written)
        while (kept.size > KEPT_READS) kept.removeFirst()
    }

    private val refresher = Executors.newSingleThreadExecutor { r -> Thread(r, "tb-aim").apply { isDaemon = true } }
    private val refreshing = AtomicBoolean(false)

    fun aimAt(read: Long, window: Int, ref: Int): Box? {
        val n = synchronized(kept) { kept.firstOrNull { it.first == read }?.second?.get(window)?.kept?.get(ref) }
            ?: return null
        if (!refreshing.compareAndSet(false, true)) return null
        val job = try {
            refresher.submit<Box?> {
                try {
                    val before = identity(n)
                    if (n.refresh() && n.visibleToUser && identity(n) == before) n.bounds.takeIf { !it.isEmpty } else null
                } catch (_: Throwable) {
                    null
                } finally {
                    refreshing.set(false)
                }
            }
        } catch (_: Throwable) {
            refreshing.set(false)
            return null
        }
        return try { job.get(refreshBudgetMs, TimeUnit.MILLISECONDS) } catch (_: Throwable) { null }
    }

    private fun identity(n: UiNode) =
        listOf(n.viewId, n.text?.toString(), n.contentDescription?.toString(), n.className?.toString())

    fun coverPresent(window: Int, pkg: String, id: String, cls: String, x: Int, y: Int): Boolean {
        freshNodes(true, null)
        for (w in nodes.windows()) {
            if (w.id != window) continue
            val root = w.root() ?: continue
            if (root.packageName?.toString() != pkg) continue
            val pending = ArrayDeque<UiNode>()
            pending.add(root)
            while (pending.isNotEmpty()) {
                val n = pending.removeLast()
                val b = n.bounds
                if (n.viewId == id && n.className?.toString() == cls &&
                    x >= b.left && x < b.right && y >= b.top && y < b.bottom) return true
                for (i in n.childCount - 1 downTo 0) n.child(i)?.let { pending.add(it) }
            }
        }
        return false
    }

    fun windowList(): List<UiWindow> = try { nodes.windows() } catch (_: Throwable) { emptyList() }

    fun windowAt(windows: List<UiWindow>, x: Float, y: Float): UiWindow? = windows.firstOrNull { w ->
        val b = w.bounds
        w.type != AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY &&
            x >= b.left && x < b.right && y >= b.top && y < b.bottom
    }

    fun keyboardShown(): Boolean {
        val readAt = now()
        return sawKeyboard(try {
            nodes.windows().any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }
        } catch (_: Throwable) { false }, readAt)
    }

    @Volatile var keyboardSeenAt = -1L
        private set

    private fun sawKeyboard(shown: Boolean, readAt: Long): Boolean {
        if (shown) keyboardSeenAt = maxOf(keyboardSeenAt, readAt)
        return shown
    }
}
