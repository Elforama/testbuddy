package dev.testbuddy.agent

import android.os.SystemClock
import android.view.accessibility.AccessibilityWindowInfo

/**
 * Reads the screen as JSON: every window, top-most first, each with its visible nodes in
 * pre-order (depth-first, document order). A node:
 *
 *   {"d":2,"b":[l,t,r,b],"c":"android.widget.Button","t":"Sign in","cd":"...","id":"com.app:id/go",
 *    "h":"hint","e":"error","f":flags}
 *
 * Empty strings and zero flags are left out. Flags are bits of [Flag].
 *
 * The nodes come from [Nodes] (on the device: UiAutomation, AndroidPlatform.kt); [now] is the
 * uptime clock, for the Toast showing.
 */
class Tree(private val nodes: Nodes, private val now: () -> Long = { SystemClock.uptimeMillis() }) {
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
        const val MORE_AFTER = 8192   // can scroll forward: more below / to the right
        const val MORE_BEFORE = 16384 // can scroll backward: more above / to the left
    }

    /**
     * The node cache is not invalidated by every change: Compose and WebView virtual
     * providers skip some (so while either is seen it is dropped before every read), and a view moved by translation (sliding or
     * springing in) sends nothing at all, so a cached read shows it where it was: a read that
     * checks whether something still moves asks for [fresh] nodes.
     */
    private fun freshNodes(fresh: Boolean, profile: ReadProfile?) {
        if (!fresh && !virtualNodesSeen) return
        try { profile.measure(ReadProfile.Part.CACHE) { nodes.clearCache() } } catch (_: Throwable) {}
    }

    fun write(j: Json, allWindows: Boolean, fresh: Boolean = false, profile: ReadProfile? = null,
              prefetchChildren: Boolean = true, includeHidden: Boolean = false): Boolean {
        freshNodes(fresh, profile)
        var virtual = false
        j.key("windows").arr()
        val readAt = now() // when the windows were read: a long dump must not stamp a later time
        var windows: List<UiWindow> = readWindows(prefetchChildren, profile)
        // Window roots below already provide the complete snapshot. Only ask for
        // the active root when the platform has no windows to enumerate, or (below) when
        // none of them is a readable app window.
        val activeRoot = if (windows.isEmpty()) profile.measure(ReadProfile.Part.ROOTS) { nodes.activeRoot(prefetchChildren) } else null
        if (activeRoot != null) {
            j.obj().put("id", activeRoot.windowId).put("type", 1).put("active", true)
                .put("pkg", activeRoot.packageName)
            j.key("nodes").arr()
            virtual = walk(j, activeRoot, profile, includeHidden) or virtual
            j.endArr().endObj()
        }
        var listed = roots(windows, allWindows, profile)
        var fix: WindowFix? = null
        if (activeRoot == null) {
            val r = recoverOpened(listed, allWindows, prefetchChildren, profile)
            if (r != null) {
                listed = r.first
                fix = r.second
                windows = listed.map { it.first }
            }
        }
        // Kept only by a read that again finds the same active window unlisted.
        val unlisted = unlistedSince
        unlistedSince = null
        if (activeRoot == null && appUnread(listed)) {
            val r = recoverApp(listed, allWindows, prefetchChildren, profile, unlisted)
            listed = r.first
            fix = r.second
            windows = listed.map { it.first }
        }
        writeToast(j, windows)
        // windows() is ordered top-most first (highest layer).
        for ((w, root) in listed) {
            if (root == null) continue
            val b = w.bounds
            j.obj().put("id", w.id).put("type", w.type).put("layer", w.layer)
                .put("active", w.active).put("focused", w.focused)
                .put("pkg", root.packageName)
            if (w.title != null) j.put("title", w.title)
            j.key("b").arr().num(b.left).num(b.top).num(b.right).num(b.bottom).endArr()
            j.key("nodes").arr()
            virtual = walk(j, root, profile, includeHidden) or virtual
            j.endArr().endObj()
        }
        j.endArr()
        // For tb's trace: what the read did when it had no readable app window, or an app window
        // left out because its root could not be read. nullRoots counts the windows left out
        // of what was written; staleNullRoots, those of the list before a fix.
        val nullRoots = listed.count { it.second == null }
        val appLeftOut = listed.any { it.second == null && it.first.type == AccessibilityWindowInfo.TYPE_APPLICATION }
        if (fix != null || appLeftOut) {
            j.key("windowFix").obj().put("nullRoots", nullRoots).put("how", fix?.how ?: "none")
            if (fix != null) j.put("window", fix.window).put("staleNullRoots", fix.staleNullRoots)
            j.endObj()
        }
        virtualNodesSeen = virtual
        return sawKeyboard(windows.any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }, readAt)
    }

    private fun readWindows(prefetchChildren: Boolean, profile: ReadProfile?): List<UiWindow> = try {
        profile.measure(ReadProfile.Part.WINDOWS) { nodes.windows(prefetchChildren) }
    } catch (_: Throwable) { emptyList() }

    /** Each window to write with its root, top-most first; a root that cannot be read is null. */
    private fun roots(windows: List<UiWindow>, allWindows: Boolean, profile: ReadProfile?): List<Pair<UiWindow, UiNode?>> =
        windows.filter { allWindows || it.type != AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY }
            .map { w -> w to (try { profile.measure(ReadProfile.Part.ROOTS) { w.root() } } catch (_: Throwable) { null }) }

    /** What a read did when the window list held no readable app window (issue #135). */
    private class WindowFix(val how: String, val window: Int, val staleNullRoots: Int)

    /**
     * The active window and window ids of a read whose read past the cache found nothing more:
     * while they stay the same (an app behind a full-screen window that does not take focus),
     * the cache is not cleared again on every read.
     */
    private var futile: Pair<Int, List<Int>>? = null

    /**
     * The active window found missing from the window list, and when (uptime ms) reads began
     * to find it so: it is written from its root only once it has stayed unlisted for
     * [UNLISTED_GRACE_MS]. Right after an app starts, its window is active and readable a moment
     * before Android lists it and gives it input; a tap then is dropped (PR #201's CI run).
     */
    private var unlistedSince: Pair<Int, Long>? = null

    /**
     * No app window can be read: none is listed, or the one listed gave no root. Right after a
     * screen opens this is a moment's gap, but the window list comes from the accessibility
     * cache, which can keep a list from before the change; on a still screen no event comes to
     * replace it, and every read held the status bar alone for as long as tb waited (issue #135).
     */
    private fun appUnread(listed: List<Pair<UiWindow, UiNode?>>): Boolean =
        listed.isNotEmpty() && listed.none { it.first.type == AccessibilityWindowInfo.TYPE_APPLICATION && it.second != null }

    private fun activeRoot(prefetchChildren: Boolean, profile: ReadProfile?): UiNode? =
        try { profile.measure(ReadProfile.Part.ROOTS) { nodes.activeRoot(prefetchChildren) } } catch (_: Throwable) { null }

    /**
     * Asks for the active window's root (past the cache). When its window is not among those
     * read, the list may be stale: clears the cache and reads the windows again. If that read
     * has a readable app window, it is the screen, whatever the active root said before the
     * clear (that root may be the screen just left). If not, the active root, read again after
     * the clear, stands for the app's window: in its place when the list has the window, or
     * added when the windows read do not cover it. Returns the windows to write and what was
     * done (null: nothing to mend, as on a lock screen whose window is listed).
     */
    private fun recoverApp(listed: List<Pair<UiWindow, UiNode?>>, allWindows: Boolean, prefetchChildren: Boolean,
                           profile: ReadProfile?, unlisted: Pair<Int, Long>?): Pair<List<Pair<UiWindow, UiNode?>>, WindowFix?> {
        val first = activeRoot(prefetchChildren, profile) ?: return listed to null
        if (readable(listed, first.windowId)) return listed to null
        val staleNullRoots = listed.count { it.second == null }
        var again = listed
        var active: UiNode = first
        val key = first.windowId to listed.map { it.first.id }
        if (key != futile) {
            try { profile.measure(ReadProfile.Part.CACHE) { nodes.clearCache() } } catch (_: Throwable) {}
            again = roots(readWindows(prefetchChildren, profile), allWindows, profile)
            if (!appUnread(again)) {
                futile = null
                val app = again.first { it.first.type == AccessibilityWindowInfo.TYPE_APPLICATION && it.second != null }
                return again to WindowFix("fresh read", app.first.id, staleNullRoots)
            }
            active = activeRoot(prefetchChildren, profile) ?: return again to null
            futile = active.windowId to again.map { it.first.id }
        }
        val id = active.windowId
        if (readable(again, id)) return again to null
        // Still no app window: the active root stands for it, where the list has its window.
        val at = again.indexOfFirst { it.first.id == id }
        if (at >= 0) {
            return again.toMutableList().apply { this[at] = this[at].first to active } to
                WindowFix("active root", id, staleNullRoots)
        }
        // Not listed: added only when the windows read leave it in sight (in #135 they were the
        // status bar alone); an app behind a full-screen window that does not take focus is not.
        if (covered(active.bounds, again.filter { it.second != null }.map { it.first.bounds })) return again to null
        // And only once it has stayed unlisted for a while: a window still being added at launch
        // is listed within it; a stale list on a still screen (#135) never is.
        val t = now()
        val since = if (unlisted?.first == id) unlisted.second else t
        unlistedSince = id to since
        if (t - since < UNLISTED_GRACE_MS) return again to null
        return again + (ActiveWindow(active) to active) to WindowFix("active root", id, staleNullRoots)
    }

    /**
     * The window that opened last: the one whose window-state event came last ([Events]), null
     * when none has yet. On the device the agent sets it; a tree read alone has no events.
     */
    @Volatile var openedWindow: () -> OpenedWindow? = { null }

    /** The opened window a read past the cache last looked for, and when (uptime ms). */
    private var staleFor = -1
    private var staleTriedAt = 0L

    /** An opened window found gone (a menu that closed): not looked for again. */
    private var goneWindow = -1

    /**
     * The window that opened last is not in the window list, which has an app window: the list
     * may be the accessibility cache's from before the change, still holding the screen just
     * left (issue #211). Its root comes from the cache too: the old screen, or an empty root once
     * that screen's content is gone. On a still screen no event comes to replace it, and every
     * read held it for as long as tb waited. Reads the windows again past the cache, at most
     * once per [STALE_RETRY_MS] for the same window. If even then the window is not listed and
     * another one is active, it is gone, and is not looked for again; a window opened only a
     * moment ago is given [STALE_RETRY_MS] to be listed before that is decided. Returns the
     * windows to write and what was done, or null when there was nothing to look for.
     */
    private fun recoverOpened(listed: List<Pair<UiWindow, UiNode?>>, allWindows: Boolean, prefetchChildren: Boolean,
                              profile: ReadProfile?): Pair<List<Pair<UiWindow, UiNode?>>, WindowFix?>? {
        val opened = try { openedWindow() } catch (_: Throwable) { null } ?: return null
        val id = opened.id
        if (id < 0 || id == goneWindow || listed.any { it.first.id == id }) return null
        // No app window at all: recoverApp's case (#135).
        if (listed.isEmpty() || appUnread(listed)) return null
        val t = now()
        if (staleFor == id && t - staleTriedAt < STALE_RETRY_MS) return null
        staleFor = id
        staleTriedAt = t
        try { profile.measure(ReadProfile.Part.CACHE) { nodes.clearCache() } } catch (_: Throwable) {}
        val again = roots(readWindows(prefetchChildren, profile), allWindows, profile)
        if (readable(again, id)) return again to WindowFix("stale list", id, listed.count { it.second == null })
        if (t - opened.at >= STALE_RETRY_MS && activeRoot(prefetchChildren, profile)?.windowId != id) goneWindow = id
        return again to null
    }

    companion object {
        /** How long an active window must stay unlisted before its root is written (see [unlistedSince]). */
        const val UNLISTED_GRACE_MS = 1500L
        /** How often a read looks again, past the cache, for an opened window not listed (see [recoverOpened]). */
        const val STALE_RETRY_MS = 1000L
    }

    private fun readable(l: List<Pair<UiWindow, UiNode?>>, id: Int) = l.any { it.first.id == id && it.second != null }

    /** Whether [boxes] cover nearly all of [b] (90% of a 16 by 16 grid of points in it). */
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

    /** The active window when the list does not have it, the size of its root. */
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

    /**
     * The Toast showing (see [Toasts]), as a window of its own on top: an android.widget.Toast
     * node per text, where Android shows a Toast by default (centred, near the bottom).
     */
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

    /** Writes the subtree's visible nodes. Returns whether it holds a virtual-node provider. */
    private fun walk(j: Json, root: UiNode, profile: ReadProfile?, includeHidden: Boolean): Boolean {
        var virtual = false
        val nodes = ArrayDeque<UiNode>()
        val depths = ArrayDeque<Int>()
        val inWeb = ArrayDeque<Boolean>()
        nodes.addLast(root); depths.addLast(0); inWeb.addLast(false)
        while (nodes.isNotEmpty()) {
            val n = nodes.removeLast()
            val depth = depths.removeLast()
            val cls = n.className?.toString()
            // A WebView's content is often reported invisible: keep all of it.
            val web = inWeb.removeLast() || (cls != null && cls.contains("WebView"))
            if (web || cls?.startsWith("androidx.compose.ui.platform.") == true) virtual = true
            writeNode(j, n, depth, cls)
            if (profile != null) profile.nodes++
            for (i in n.childCount - 1 downTo 0) {
                val c = try { profile.measure(ReadProfile.Part.CHILDREN) { n.child(i) } } catch (_: Throwable) { null } ?: continue
                if (!includeHidden && !web && !c.visibleToUser) continue
                nodes.addLast(c); depths.addLast(depth + 1); inWeb.addLast(web)
            }
        }
        return virtual
    }

    private fun writeNode(j: Json, n: UiNode, depth: Int, cls: String?) {
        val bounds = n.bounds
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
        // A list's size and an item's place in it: how much of the list is off screen.
        n.collection?.let { j.key("ci").arr().num(it[0]).num(it[1]).endArr() }
        n.collectionItem?.let { j.key("ii").arr().num(it[0]).num(it[1]).endArr() }
        // Its place among its siblings as drawn: the tree lists by place, not as drawn, so tb
        // needs it to tell a layer under an element from one over it.
        val order = n.drawingOrder
        if (order >= 0) j.put("o", order)
        j.endObj()
    }

    /** The field with input focus, for typing fallbacks. */
    fun focusedInput(): Field? = try { nodes.focusedInput() } catch (_: Throwable) { null }

    /** Presence at a point, including invisible nodes that may still take input. */
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

    /**
     * The id of the top-most window at ([x], [y]), the one a touch there goes to; -1 when none
     * or the windows cannot be read. Accessibility overlays (tb's own, a screen reader's) are
     * passed over: they do not take touches.
     */
    fun windowAt(x: Float, y: Float): Int = try {
        nodes.windows().firstOrNull { w ->
            val b = w.bounds
            w.type != AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY &&
                x >= b.left && x < b.right && y >= b.top && y < b.bottom
        }?.id ?: -1
    } catch (_: Throwable) { -1 }

    fun keyboardShown(): Boolean {
        val readAt = now()
        return sawKeyboard(try {
            nodes.windows().any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }
        } catch (_: Throwable) { false }, readAt)
    }

    /** Uptime ms when a read last found the keyboard up, or -1 (hideKeyboard's wait, issue #175). */
    @Volatile var keyboardSeenAt = -1L
        private set

    private fun sawKeyboard(shown: Boolean, readAt: Long): Boolean {
        if (shown) keyboardSeenAt = maxOf(keyboardSeenAt, readAt)
        return shown
    }
}
