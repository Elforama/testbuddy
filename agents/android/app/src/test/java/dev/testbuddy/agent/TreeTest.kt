package dev.testbuddy.agent

import android.view.accessibility.AccessibilityWindowInfo
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The screen as tb reads it: the JSON the agent writes from a fake accessibility tree. */
class TreeTest {
    private val nodes = FakeNodes()
    private var now = 0L
    private val tree = Tree(nodes) { now }

    @After fun noToast() { Toasts.texts = emptyList(); Toasts.until = 0 }

    @Test
    fun windowSnapshotsDoNotFetchAnUnusedActiveRoot() {
        nodes.windowList = listOf(app(FakeNode("android.widget.TextView", "Window content")))
        nodes.active = FakeNode("android.widget.TextView", "Unused content")
        assertEquals("Window content", read().getJSONObject(0).getJSONArray("nodes").getJSONObject(0).getString("t"))
        assertEquals(0, nodes.activeRootReads)
    }

    @Test
    fun emptyWindowListStillUsesTheActiveRootFallback() {
        nodes.active = FakeNode("android.widget.TextView", "Fallback content")
        assertEquals("Fallback content", read().getJSONObject(0).getJSONArray("nodes").getJSONObject(0).getString("t"))
        assertEquals(1, nodes.activeRootReads)
    }

    private fun read(all: Boolean = false, fresh: Boolean = false): JSONArray {
        val j = Json().obj()
        tree.write(j, all, fresh)
        return JSONObject(j.endObj().toString()).getJSONArray("windows")
    }

    private fun app(root: FakeNode, id: Int = 1) = FakeWindow(id, AccessibilityWindowInfo.TYPE_APPLICATION, root, active = true, focused = true, layer = 1)

    private fun JSONArray.objects() = (0 until length()).map { getJSONObject(it) }

    @Test
    fun nodesInPreOrderWithTheirDepth() {
        val root = FakeNode("android.widget.FrameLayout", children = listOf(
            FakeNode("android.widget.LinearLayout", children = listOf(FakeNode("android.widget.TextView", "A"))),
            FakeNode("android.widget.Button", "B"),
        ))
        nodes.windowList = listOf(app(root))
        val list = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertEquals(listOf(0, 1, 2, 1), list.map { it.getInt("d") })
        assertEquals(listOf("", "", "A", "B"), list.map { it.optString("t") })
    }

    @Test
    fun aWindowsFields() {
        nodes.windowList = listOf(FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 100, 1080, 2400),
            layer = 3, active = true, focused = false, title = "Home"))
        val w = read().getJSONObject(0)
        assertEquals(5, w.getInt("id"))
        assertEquals(3, w.getInt("layer"))
        assertTrue(w.getBoolean("active"))
        assertFalse(w.getBoolean("focused"))
        assertEquals("com.app", w.getString("pkg"))
        assertEquals("Home", w.getString("title"))
        assertEquals("[0,100,1080,2400]", w.getJSONArray("b").toString())
    }

    @Test
    fun hiddenChildrenAreLeftOutButAWebViewKeepsAll() {
        val root = FakeNode(children = listOf(
            FakeNode("android.widget.TextView", "hidden", visibleToUser = false),
            FakeNode("android.webkit.WebView", children = listOf(FakeNode("android.view.View", "in page", visibleToUser = false))),
        ))
        nodes.windowList = listOf(app(root))
        val texts = read().getJSONObject(0).getJSONArray("nodes").objects().map { it.optString("t") }
        assertFalse(texts.contains("hidden"))
        assertTrue(texts.contains("in page"))
    }

    @Test
    fun flagsMatchTheNodesState() {
        val n = FakeNode("android.widget.EditText", "x").apply {
            clickable = true; focused = true; editable = true; password = true; showingHint = false
            scrollable = true; canScrollForward = true
        }
        nodes.windowList = listOf(app(FakeNode(children = listOf(n))))
        val f = read().getJSONObject(0).getJSONArray("nodes").getJSONObject(1).getInt("f")
        val want = Tree.Flag.CLICKABLE or Tree.Flag.ENABLED or Tree.Flag.FOCUSED or Tree.Flag.EDITABLE or
            Tree.Flag.PASSWORD or Tree.Flag.SCROLLABLE or Tree.Flag.MORE_AFTER or Tree.Flag.VISIBLE
        assertEquals(want, f)
    }

    @Test
    fun scrollActionsCountOnlyOnAScrollableNode() {
        val n = FakeNode().apply { canScrollForward = true; canScrollBackward = true }
        nodes.windowList = listOf(app(n))
        val f = read().getJSONObject(0).getJSONArray("nodes").getJSONObject(0).getInt("f")
        assertEquals(0, f and (Tree.Flag.MORE_AFTER or Tree.Flag.MORE_BEFORE))
    }

    @Test
    fun emptyStringsAreLeftOutAndListsSayTheirSize() {
        val n = FakeNode("android.widget.ListView", "").apply {
            contentDescription = "Results"; viewId = "com.app:id/list"; hint = ""; error = "Required"; stateDescription = "On"
            collection = intArrayOf(40, 1)
        }
        val item = FakeNode("android.widget.TextView", "Row 3").apply { collectionItem = intArrayOf(3, 0) }
        n.children = listOf(item)
        nodes.windowList = listOf(app(n))
        val (list, row) = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertFalse(list.has("t"))
        assertFalse(list.has("h"))
        assertEquals("Results", list.getString("cd"))
        assertEquals("com.app:id/list", list.getString("id"))
        assertEquals("Required", list.getString("e"))
        assertEquals("On", list.getString("sd"))
        assertEquals("[40,1]", list.getJSONArray("ci").toString())
        assertEquals("[3,0]", row.getJSONArray("ii").toString())
    }

    @Test
    fun overlaysAreLeftOutUnlessAllWindowsAreAskedFor() {
        nodes.windowList = listOf(
            FakeWindow(9, AccessibilityWindowInfo.TYPE_ACCESSIBILITY_OVERLAY, FakeNode()),
            app(FakeNode()),
        )
        assertEquals(listOf(1), read().objects().map { it.getInt("id") })
        assertEquals(listOf(9, 1), read(all = true).objects().map { it.getInt("id") })
    }

    @Test
    fun aWindowWithNoRootIsLeftOut() {
        nodes.windowList = listOf(FakeWindow(2, AccessibilityWindowInfo.TYPE_SYSTEM, null), app(FakeNode()))
        assertEquals(listOf(1), read().objects().map { it.getInt("id") })
    }

    @Test
    fun withNoWindowsListTheActiveRootStandsIn() {
        nodes.active = FakeNode().apply { windowId = 12 }
        val w = read().getJSONObject(0)
        assertEquals(12, w.getInt("id"))
        assertTrue(w.getBoolean("active"))
        assertEquals("com.app", w.getString("pkg"))
    }

    @Test
    fun aToastShowsAsAWindowOnTop() {
        Toasts.texts = listOf("Saved")
        Toasts.until = 3_500
        nodes.windowList = listOf(app(FakeNode()))
        val (toast, app) = read().objects()
        assertEquals(-2, toast.getInt("id"))
        assertEquals("Toast", toast.getString("title"))
        val node = toast.getJSONArray("nodes").getJSONObject(0)
        assertEquals("android.widget.Toast", node.getString("c"))
        assertEquals("Saved", node.getString("t"))
        // Centred, near the bottom of the screen.
        val b = node.getJSONArray("b")
        assertEquals(1080 - b.getInt(2), b.getInt(0))
        assertEquals(2400 - 2400 / 8 - 2400 / 20, b.getInt(1))
        assertEquals(1, app.getInt("id"))
        now = 3_500
        assertEquals(1, read().length())
    }

    @Test
    fun onceComposeIsSeenEveryReadDropsTheCache() {
        nodes.windowList = listOf(app(FakeNode("android.widget.FrameLayout")))
        read(); read()
        assertEquals(0, nodes.cacheClears)
        read(fresh = true)
        assertEquals(1, nodes.cacheClears)
        nodes.windowList = listOf(app(FakeNode("androidx.compose.ui.platform.ComposeView")))
        read()
        read()
        assertEquals(2, nodes.cacheClears)
    }

    @Test
    fun webViewTextChangesWithoutEventsAreReadFresh() {
        val field = FakeNode("android.widget.EditText", "")
        nodes.windowList = listOf(app(FakeNode("android.webkit.WebView", children = listOf(field))))
        read()
        // Chromium changed its virtual input node, but Android still holds the old
        // prefetched node. Missing events must not cause first-key verification to
        // replay an already delivered character ("hi" became "hhi" on device).
        nodes.onClearCache = { field.text = "h" }
        val snapshot = read().getJSONObject(0).getJSONArray("nodes")
        assertEquals("h", snapshot.getJSONObject(1).optString("t"))
    }

    @Test
    fun theKeyboardIsAnInputMethodWindow() {
        nodes.windowList = listOf(app(FakeNode()))
        assertFalse(tree.keyboardShown())
        nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()), app(FakeNode()))
        assertTrue(tree.keyboardShown())
    }

    /** Each node's drawing order ("o") goes to tb, so it can tell a layer drawn under a tap
     *  target from one over it (Issue #11); an unknown order (-1) is left out. */
    @Test
    fun drawingOrder() {
        val bar = FakeNode("android.widget.LinearLayout").apply { drawingOrder = 1 }
        val layer = FakeNode("android.widget.FrameLayout").apply { drawingOrder = 0 }
        val unknown = FakeNode("android.view.View")
        nodes.windowList = listOf(app(FakeNode(children = listOf(bar, layer, unknown))))
        val list = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertEquals(listOf(false, true, true, false), list.map { it.has("o") })
        assertEquals(1, list[1].getInt("o"))
        assertEquals(0, list[2].getInt("o"))
    }

    // Issue #135: in CI, after a tap opened a new screen, every read for 17 s held the status
    // bar alone, while the screen showed the app. The window list read through the
    // accessibility cache had no app window whose root could be read, and nothing in the
    // read noticed.

    private fun statusBar() = FakeWindow(2, AccessibilityWindowInfo.TYPE_SYSTEM,
        FakeNode("android.widget.TextView", "2:19").apply { packageName = "com.android.systemui"; windowId = 2 },
        Box(0, 0, 1080, 63), layer = 5)

    private fun emptyScreen(windowId: Int = 7) = FakeNode("android.widget.FrameLayout", bounds = Box(0, 0, 1080, 2400),
        children = listOf(FakeNode("android.widget.TextView", "Nothing here yet").apply { this.windowId = windowId }))
        .apply { packageName = "dev.testbuddy.bench"; this.windowId = windowId }

    private fun readWithFix(): JSONObject {
        val j = Json().obj()
        tree.write(j, false)
        return JSONObject(j.endObj().toString())
    }

    private fun JSONObject.texts() = getJSONArray("windows").objects()
        .flatMap { w -> w.getJSONArray("nodes").objects().map { it.optString("t") } }

    /** Issue #135: the app's window is listed but its root comes back null, while the active
     *  window's root has the app: the read must hold the app, not the status bar alone. */
    @Test
    fun anAppWindowWithANullRootIsReadThroughTheActiveRoot() {
        nodes.windowList = listOf(statusBar(), FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, null, active = true, focused = true, layer = 1))
        nodes.active = emptyScreen()
        val r = readWithFix()
        assertTrue(r.texts().contains("Nothing here yet"))
        assertEquals(1, r.texts().count { it == "Nothing here yet" })
        val fix = r.getJSONObject("windowFix")
        assertEquals(1, fix.getInt("nullRoots"))
        assertEquals("active root", fix.getString("how"))
        assertEquals(1, nodes.cacheClears) // a fresh read was tried first
    }

    /** Issue #135: the cached window list lacks the app's window; a read past the cache has it. */
    @Test
    fun aCachedWindowListWithoutTheAppIsReadAgainPastTheCache() {
        val fresh = listOf(statusBar(), FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, emptyScreen(), active = true, focused = true, layer = 1))
        nodes.windowList = listOf(statusBar())
        nodes.onClearCache = { nodes.windowList = fresh }
        nodes.active = emptyScreen()
        val r = readWithFix()
        assertEquals(1, r.texts().count { it == "Nothing here yet" })
        assertEquals(1, nodes.cacheClears)
        assertEquals(2, nodes.windowReads)
        val fix = r.getJSONObject("windowFix")
        assertEquals(0, fix.getInt("nullRoots"))
        assertEquals("fresh read", fix.getString("how"))
    }

    /** The check costs nothing on a normal read: an app window is listed, so neither the active
     *  root nor a fresh read is asked for, and nothing is reported. */
    @Test
    fun aNormalReadDoesNoExtraWork() {
        nodes.windowList = listOf(statusBar(), FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, emptyScreen(), active = true, focused = true, layer = 1))
        nodes.active = emptyScreen()
        val r = readWithFix()
        assertEquals(0, nodes.activeRootReads)
        assertEquals(0, nodes.cacheClears)
        assertEquals(1, nodes.windowReads)
        assertFalse(r.has("windowFix"))
    }

    /** No app window, but the active window is a listed one (a lock screen, the shade): it is
     *  already read, so nothing is cleared or written twice. */
    @Test
    fun anActiveWindowAlreadyListedIsNotReadAgain() {
        val shade = FakeNode("android.widget.TextView", "Shade").apply { packageName = "com.android.systemui"; windowId = 2 }
        nodes.windowList = listOf(FakeWindow(2, AccessibilityWindowInfo.TYPE_SYSTEM, shade, active = true))
        nodes.active = shade
        val r = readWithFix()
        assertEquals(0, nodes.cacheClears)
        assertEquals(1, r.texts().count { it == "Shade" })
        assertFalse(r.has("windowFix"))
    }
}
