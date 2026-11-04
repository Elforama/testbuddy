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
    fun theKeyboardIsAnInputMethodWindow() {
        nodes.windowList = listOf(app(FakeNode()))
        assertFalse(tree.keyboardShown())
        nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()), app(FakeNode()))
        assertTrue(tree.keyboardShown())
    }
}
