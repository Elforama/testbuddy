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
        val windows: List<UiWindow> = try {
            profile.measure(ReadProfile.Part.WINDOWS) { nodes.windows(prefetchChildren) }
        } catch (_: Throwable) { emptyList() }
        // Window roots below already provide the complete snapshot. Only ask for
        // the active root when the platform has no windows to enumerate.
        val activeRoot = if (windows.isEmpty()) profile.measure(ReadProfile.Part.ROOTS) { nodes.activeRoot(prefetchChildren) } else null
        if (activeRoot != null) {
            j.obj().put("id", activeRoot.windowId).put("type", 1).put("active", true)
                .put("pkg", activeRoot.packageName)
            j.key("nodes").arr()
            virtual = walk(j, activeRoot, profile, includeHidden) or virtual
            j.endArr().endObj()
        }
        writeToast(j, windows)
        // windows() is ordered top-most first (highest layer).
        for (w in windows) {
            if (!allWindows && w.type == AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY) continue
            val root = try { profile.measure(ReadProfile.Part.ROOTS) { w.root() } } catch (_: Throwable) { null } ?: continue
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
        virtualNodesSeen = virtual
        return windows.any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }
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

    fun keyboardShown(): Boolean = try {
        nodes.windows().any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }
    } catch (_: Throwable) { false }
}
