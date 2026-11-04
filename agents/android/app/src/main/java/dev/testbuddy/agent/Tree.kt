package dev.testbuddy.agent

import android.accessibilityservice.AccessibilityServiceInfo
import android.app.UiAutomation
import android.graphics.Rect
import android.os.Build
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo

/**
 * Reads the screen as JSON: every window, top-most first, each with its visible nodes in
 * pre-order (depth-first, document order). A node:
 *
 *   {"d":2,"b":[l,t,r,b],"c":"android.widget.Button","t":"Sign in","cd":"...","id":"com.app:id/go",
 *    "h":"hint","e":"error","f":flags,"z":3}
 *
 * Empty strings and zero flags are left out. Flags are bits of [Flag]. "z" is the node's drawing
 * order among its siblings ([AccessibilityNodeInfo.getDrawingOrder]: elevation and custom child
 * drawing order count), which the tree order does not give.
 */
class Tree(private val ua: UiAutomation) {
    private val bounds = Rect()
    private var composeSeen = false

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

    init {
        val info = ua.serviceInfo
        info.flags = info.flags or
            AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS or
            AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS or
            AccessibilityServiceInfo.FLAG_INCLUDE_NOT_IMPORTANT_VIEWS
        ua.serviceInfo = info
    }

    /**
     * The node cache is not invalidated by every change: Compose skips some (so once Compose
     * was seen it is dropped before every read), and a view moved by translation (sliding or
     * springing in) sends nothing at all, so a cached read shows it where it was: a read that
     * checks whether something still moves asks for [fresh] nodes.
     */
    private fun freshNodes(fresh: Boolean) {
        if (!fresh && !composeSeen) return
        try {
            if (Build.VERSION.SDK_INT >= 34) ua.clearCache()
            else ua.serviceInfo = ua.serviceInfo
        } catch (_: Throwable) {}
    }

    fun write(j: Json, allWindows: Boolean, fresh: Boolean = false) {
        freshNodes(fresh)
        var compose = false
        j.key("windows").arr()
        val windows: List<AccessibilityWindowInfo> = try { ua.windows } catch (_: Throwable) { emptyList() }
        val activeRoot = ua.rootInActiveWindow
        if (windows.isEmpty() && activeRoot != null) {
            j.obj().put("id", activeRoot.windowId).put("type", 1).put("active", true)
                .put("pkg", activeRoot.packageName)
            j.key("nodes").arr()
            compose = walk(j, activeRoot) or compose
            j.endArr().endObj()
        }
        writeToast(j, windows)
        // ua.windows is ordered top-most first (highest layer).
        for (w in windows) {
            if (!allWindows && w.type == AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY) continue
            val root = rootOf(w) ?: continue
            w.getBoundsInScreen(bounds)
            j.obj().put("id", w.id).put("type", w.type).put("layer", w.layer)
                .put("active", w.isActive).put("focused", w.isFocused)
                .put("pkg", root.packageName)
            if (w.title != null) j.put("title", w.title)
            j.key("b").arr().num(bounds.left).num(bounds.top).num(bounds.right).num(bounds.bottom).endArr()
            j.key("nodes").arr()
            compose = walk(j, root) or compose
            j.endArr().endObj()
        }
        j.endArr()
        composeSeen = compose
    }

    /**
     * The Toast showing (see [Toasts]), as a window of its own on top: an android.widget.Toast
     * node per text, where Android shows a Toast by default (centred, near the bottom).
     */
    private fun writeToast(j: Json, windows: List<AccessibilityWindowInfo>) {
        val texts = Toasts.showing()
        if (texts.isEmpty()) return
        val screen = Rect()
        for (w in windows) {
            w.getBoundsInScreen(bounds)
            screen.union(bounds)
        }
        if (screen.isEmpty) return
        val h = screen.height() / 20
        val w = screen.width() * 2 / 3
        val l = screen.centerX() - w / 2
        var t = screen.bottom - screen.height() / 8 - h * texts.size
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

    private fun rootOf(w: AccessibilityWindowInfo): AccessibilityNodeInfo? = try {
        if (Build.VERSION.SDK_INT >= 33) {
            w.getRoot(
                AccessibilityNodeInfo.FLAG_PREFETCH_DESCENDANTS_HYBRID or
                    AccessibilityNodeInfo.FLAG_PREFETCH_UNINTERRUPTIBLE,
            )
        } else w.root
    } catch (_: Throwable) { null }

    /** Writes the subtree's visible nodes. Returns whether it holds Compose content. */
    private fun walk(j: Json, root: AccessibilityNodeInfo): Boolean {
        var compose = false
        val nodes = ArrayDeque<AccessibilityNodeInfo>()
        val depths = ArrayDeque<Int>()
        val inWeb = ArrayDeque<Boolean>()
        nodes.addLast(root); depths.addLast(0); inWeb.addLast(false)
        while (nodes.isNotEmpty()) {
            val n = nodes.removeLast()
            val depth = depths.removeLast()
            val cls = n.className?.toString()
            // A WebView's content is often reported invisible: keep all of it.
            val web = inWeb.removeLast() || (cls != null && cls.contains("WebView"))
            if (!compose && cls != null && cls.startsWith("androidx.compose.ui.platform.")) compose = true
            writeNode(j, n, depth, cls)
            for (i in n.childCount - 1 downTo 0) {
                val c = try { n.getChild(i) } catch (_: Throwable) { null } ?: continue
                if (!web && !c.isVisibleToUser) continue
                nodes.addLast(c); depths.addLast(depth + 1); inWeb.addLast(web)
            }
        }
        return compose
    }

    private fun writeNode(j: Json, n: AccessibilityNodeInfo, depth: Int, cls: String?) {
        n.getBoundsInScreen(bounds)
        j.obj().put("d", depth)
        j.key("b").arr().num(bounds.left).num(bounds.top).num(bounds.right).num(bounds.bottom).endArr()
        if (cls != null) j.put("c", cls)
        val text = n.text
        if (!text.isNullOrEmpty()) j.put("t", text)
        val cd = n.contentDescription
        if (!cd.isNullOrEmpty()) j.put("cd", cd)
        val id = n.viewIdResourceName
        if (!id.isNullOrEmpty()) j.put("id", id)
        val hint = n.hintText
        if (!hint.isNullOrEmpty()) j.put("h", hint)
        val err = n.error
        if (!err.isNullOrEmpty()) j.put("e", err)
        if (Build.VERSION.SDK_INT >= 30) {
            val sd = n.stateDescription
            if (!sd.isNullOrEmpty()) j.put("sd", sd)
        }
        var f = 0
        if (n.isClickable) f = f or Flag.CLICKABLE
        if (n.isEnabled) f = f or Flag.ENABLED
        if (n.isFocused) f = f or Flag.FOCUSED
        if (n.isScrollable) f = f or Flag.SCROLLABLE
        if (n.isChecked) f = f or Flag.CHECKED
        if (n.isSelected) f = f or Flag.SELECTED
        if (n.isCheckable) f = f or Flag.CHECKABLE
        if (n.isPassword) f = f or Flag.PASSWORD
        if (n.isEditable) f = f or Flag.EDITABLE
        if (n.isLongClickable) f = f or Flag.LONG_CLICKABLE
        if (n.isFocusable) f = f or Flag.FOCUSABLE
        if (n.isShowingHintText) f = f or Flag.SHOWING_HINT
        if (n.isVisibleToUser) f = f or Flag.VISIBLE
        if (n.isScrollable) {
            for (a in n.actionList) {
                when (a.id) {
                    AccessibilityNodeInfo.ACTION_SCROLL_FORWARD,
                    AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_DOWN.id,
                    AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_RIGHT.id -> f = f or Flag.MORE_AFTER
                    AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD,
                    AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_UP.id,
                    AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_LEFT.id -> f = f or Flag.MORE_BEFORE
                }
            }
        }
        if (f != 0) j.put("f", f)
        val z = n.drawingOrder
        if (z != 0) j.put("z", z)
        // A list's size and an item's place in it: how much of the list is off screen.
        n.collectionInfo?.let { j.key("ci").arr().num(it.rowCount).num(it.columnCount).endArr() }
        n.collectionItemInfo?.let { j.key("ii").arr().num(it.rowIndex).num(it.columnIndex).endArr() }
        j.endObj()
    }

    /** The node with input focus, for typing fallbacks. */
    fun focusedInput(): AccessibilityNodeInfo? = try { ua.findFocus(AccessibilityNodeInfo.FOCUS_INPUT) } catch (_: Throwable) { null }

    fun keyboardShown(): Boolean = try {
        ua.windows.any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD }
    } catch (_: Throwable) { false }
}
