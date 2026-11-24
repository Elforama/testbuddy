package dev.testbuddy.agent

import android.view.accessibility.AccessibilityWindowInfo
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

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
    fun visibleNodesUnderAHiddenInteropHolderAreListed() {
        val root = FakeNode("androidx.compose.ui.platform.ComposeView", bounds = Box(0, 433, 1080, 2400), children = listOf(
            FakeNode("androidx.compose.ui.viewinterop.ViewFactoryHolder", bounds = Box(0, 433, 1080, 2400), visibleToUser = false, children = listOf(
                FakeNode("android.widget.FrameLayout", bounds = Box(0, 433, 1080, 2400), children = listOf(
                    FakeNode("android.widget.TextView", "in pager", Box(496, 1597, 844, 1648)),
                )),
            )),
            FakeNode("android.widget.TextView", "after", Box(0, 2300, 100, 2400)),
        ))
        nodes.windowList = listOf(app(root))
        val list = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertEquals(listOf("androidx.compose.ui.platform.ComposeView", "android.widget.FrameLayout", "android.widget.TextView", "android.widget.TextView"),
            list.map { it.getString("c") })
        assertEquals(listOf("", "", "in pager", "after"), list.map { it.optString("t") })
        assertEquals(listOf(0, 1, 2, 1), list.map { it.getInt("d") })
    }

    @Test
    fun visibleNodesUnderAnyHiddenNodeAreListedAndHiddenOnesAreNot() {
        val root = FakeNode("android.widget.FrameLayout", bounds = Box(0, 0, 1080, 2400), children = listOf(
            FakeNode("android.widget.FrameLayout", visibleToUser = false, children = listOf(
                FakeNode("android.widget.TextView", "shown"),
                FakeNode("android.widget.LinearLayout", visibleToUser = false, children = listOf(
                    FakeNode("android.widget.TextView", "hidden too", visibleToUser = false),
                    FakeNode("android.widget.TextView", "deep"),
                )),
            )),
        ))
        nodes.windowList = listOf(app(root))
        val list = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertEquals(listOf("", "shown", "deep"), list.map { it.optString("t") })
        assertEquals(listOf(0, 1, 1), list.map { it.getInt("d") })
        assertTrue(list.all { it.getInt("f") and Tree.Flag.VISIBLE != 0 })
    }

    @Test
    fun underAHiddenNodeOnlyWhatIsOnScreenIsListed() {
        val root = FakeNode("android.widget.FrameLayout", bounds = Box(0, 0, 1080, 2400), children = listOf(
            FakeNode("android.view.View", bounds = Box(1080, 0, 2160, 2400), visibleToUser = false, children = listOf(
                FakeNode("android.widget.TextView", "next page", Box(1200, 100, 1400, 200)),
                FakeNode("android.widget.TextView", "stray", Box(100, 100, 200, 200)),
            )),
            FakeNode("android.view.View", bounds = Box(0, 0, 1080, 2400), visibleToUser = false, children = listOf(
                FakeNode("android.widget.TextView", "off", Box(-300, 100, -100, 200)),
                FakeNode("android.widget.TextView", "empty", Box(100, 100, 100, 100)),
                FakeNode("android.widget.TextView", "on", Box(100, 100, 200, 200)),
            )),
        ))
        nodes.windowList = listOf(app(root))
        val texts = read().getJSONObject(0).getJSONArray("nodes").objects().map { it.optString("t") }
        assertEquals(listOf("", "on"), texts)
    }

    @Test
    fun underAHiddenNodeTheNodesWrittenAboveClipWhatIsListed() {
        val root = FakeNode("android.widget.FrameLayout", bounds = Box(0, 0, 1080, 2400), children = listOf(
            FakeNode("android.view.View", "list", Box(0, 0, 1080, 1000), children = listOf(
                FakeNode("androidx.compose.ui.viewinterop.ViewFactoryHolder", bounds = Box(0, 0, 1080, 1200), visibleToUser = false, children = listOf(
                    FakeNode("android.widget.TextView", "half", Box(0, 900, 100, 1100)),
                    FakeNode("android.widget.TextView", "below", Box(0, 1100, 100, 1200)),
                )),
            )),
        ))
        nodes.windowList = listOf(app(root))
        val list = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertEquals(listOf("", "list", "half"), list.map { it.optString("t") })
        assertEquals("[0,900,100,1000]", list[2].getJSONArray("b").toString())
    }

    @Test
    fun aLiftedNodeTakesTheDrawingOrderOfItsTopMostHiddenAncestor() {
        val inner = FakeNode("android.widget.TextView", "inner").apply { drawingOrder = 0 }
        val lifted = FakeNode("android.widget.FrameLayout", "lifted", children = listOf(inner)).apply { drawingOrder = 0 }
        val holder = FakeNode("android.view.View", visibleToUser = false, children = listOf(
            FakeNode("android.view.View", visibleToUser = false, children = listOf(lifted)).apply { drawingOrder = 4 },
        )).apply { drawingOrder = 2 }
        val root = FakeNode("android.widget.FrameLayout", children = listOf(
            FakeNode("android.widget.TextView", "sibling").apply { drawingOrder = 1 },
            holder,
        ))
        nodes.windowList = listOf(app(root))
        val list = read().getJSONObject(0).getJSONArray("nodes").objects()
        assertEquals(listOf("", "sibling", "lifted", "inner"), list.map { it.optString("t") })
        assertEquals(listOf(1, 2, 0), list.drop(1).map { it.getInt("o") })
    }

    @Test
    fun aHiddenSubtreeListsNothing() {
        val root = FakeNode("android.widget.FrameLayout", children = listOf(
            FakeNode("android.widget.FrameLayout", visibleToUser = false, children = listOf(
                FakeNode("android.widget.TextView", "a", visibleToUser = false),
                FakeNode("android.widget.LinearLayout", visibleToUser = false, children = listOf(
                    FakeNode("android.widget.TextView", "b", visibleToUser = false))),
            )),
        ))
        nodes.windowList = listOf(app(root))
        assertEquals(1, read().getJSONObject(0).getJSONArray("nodes").length())
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

    @Test
    fun anAppWindowWithANullRootIsReadThroughTheActiveRoot() {
        nodes.windowList = listOf(statusBar(), FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, null, active = true, focused = true, layer = 1))
        nodes.active = emptyScreen()
        val r = readWithFix()
        assertTrue(r.texts().contains("Nothing here yet"))
        assertEquals(1, r.texts().count { it == "Nothing here yet" })
        val fix = r.getJSONObject("windowFix")
        assertEquals(1, fix.getInt("staleNullRoots"))
        assertEquals(0, fix.getInt("nullRoots"))
        assertEquals("active root", fix.getString("how"))
        assertEquals(1, nodes.cacheClears)
    }

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
        assertEquals(7, fix.getInt("window"))
    }

    @Test
    fun aFreshReadWithTheAppWinsOverAnActiveRootFromBeforeTheClear() {
        val old = FakeNode("android.widget.TextView", "Old screen", bounds = Box(0, 0, 1080, 2400)).apply { windowId = 5 }
        val new = FakeNode("android.widget.TextView", "New screen", bounds = Box(0, 0, 1080, 2400)).apply { windowId = 7 }
        nodes.windowList = listOf(statusBar())
        nodes.active = old
        nodes.onClearCache = {
            nodes.windowList = listOf(statusBar(), FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, new, active = true, focused = true, layer = 1))
            nodes.active = new
        }
        val r = readWithFix()
        assertEquals(listOf("2:19", "New screen"), r.texts())
        assertEquals("fresh read", r.getJSONObject("windowFix").getString("how"))
        assertEquals(7, r.getJSONObject("windowFix").getInt("window"))
    }

    @Test
    fun anUnlistedActiveWindowLeftInSightIsWrittenFromItsRootAfterTheClear() {
        val before = emptyScreen(5).apply { children = listOf(FakeNode("android.widget.TextView", "Old screen")) }
        val after = emptyScreen(7).apply { windowType = AccessibilityWindowInfo.TYPE_APPLICATION }
        nodes.windowList = listOf(statusBar())
        nodes.active = before
        nodes.onClearCache = { nodes.active = after }
        assertEquals(listOf("2:19"), readWithFix().texts())
        now += Tree.UNLISTED_GRACE_MS
        val r = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        val w = r.getJSONArray("windows").objects().last()
        assertEquals(7, w.getInt("id"))
        assertEquals(AccessibilityWindowInfo.TYPE_APPLICATION, w.getInt("type"))
        assertEquals("active root", r.getJSONObject("windowFix").getString("how"))
    }

    @Test
    fun anUnlistedActiveWindowIsNotWrittenBeforeTheGracePeriod() {
        nodes.windowList = listOf(statusBar())
        nodes.active = emptyScreen()
        for (t in listOf(0L, 500L, Tree.UNLISTED_GRACE_MS - 1)) {
            now = t
            val r = readWithFix()
            assertEquals(listOf("2:19"), r.texts())
            assertEquals("pending", r.getJSONObject("windowFix").getString("how"))
            assertTrue(r.getJSONObject("windowFix").getBoolean("pending"))
        }
        now = Tree.UNLISTED_GRACE_MS
        val r = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        assertEquals("active root", r.getJSONObject("windowFix").getString("how"))
    }

    @Test
    fun aWindowListedDuringTheGracePeriodIsUsedAndTheTimerStartsOver() {
        val app = FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, emptyScreen(), active = true, focused = true, layer = 1)
        nodes.windowList = listOf(statusBar())
        nodes.active = emptyScreen()
        now = 0
        assertEquals(listOf("2:19"), readWithFix().texts())
        now = 1000
        nodes.windowList = listOf(statusBar(), app)
        val listed = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), listed.texts())
        assertFalse(listed.has("windowFix"))
        now = 1200
        nodes.windowList = listOf(statusBar())
        assertEquals(listOf("2:19"), readWithFix().texts())
        now = 1200 + Tree.UNLISTED_GRACE_MS - 1
        assertEquals(listOf("2:19"), readWithFix().texts())
        now = 1200 + Tree.UNLISTED_GRACE_MS
        val r = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        assertEquals("active root", r.getJSONObject("windowFix").getString("how"))
    }

    @Test
    fun aCoveredActiveWindowIsNotAddedAndNotClearedForAgain() {
        val overlay = FakeNode("android.widget.TextView", "Overlay", bounds = Box(0, 0, 1080, 2400)).apply { windowId = 9 }
        nodes.windowList = listOf(FakeWindow(9, AccessibilityWindowInfo.TYPE_SYSTEM, overlay, layer = 9))
        nodes.active = emptyScreen()
        repeat(3) {
            val r = readWithFix()
            assertEquals(listOf("Overlay"), r.texts())
            assertFalse(r.has("windowFix"))
        }
        assertEquals(1, nodes.cacheClears)
    }

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

    private fun oldScreen() = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION,
        FakeNode("android.widget.FrameLayout", bounds = Box(0, 0, 1080, 2400),
            children = listOf(FakeNode("android.widget.Button", "Empty list").apply { windowId = 5 }))
            .apply { packageName = "dev.testbuddy.bench"; windowId = 5 },
        active = true, focused = true, layer = 1)

    private fun newScreen() = FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, emptyScreen(7), active = true, focused = true, layer = 1)

    @Test
    fun aCachedListHoldingThePreviousScreenIsReadAgainPastTheCache() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        nodes.windowList = listOf(statusBar(), oldScreen())
        nodes.onClearCache = { nodes.windowList = listOf(statusBar(), newScreen()) }
        nodes.active = emptyScreen(7)
        now = 100
        val r = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        assertEquals(1, nodes.cacheClears)
        val fix = r.getJSONObject("windowFix")
        assertEquals("stale list", fix.getString("how"))
        assertEquals(7, fix.getInt("window"))
        val again = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), again.texts())
        assertEquals(1, nodes.cacheClears)
        assertFalse(again.has("windowFix"))
    }

    @Test
    fun aCachedListHoldingTheEmptiedPreviousWindowIsReadAgainPastTheCache() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        val emptied = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode("android.widget.FrameLayout", bounds = Box(0, 0, 1080, 2400)).apply { windowId = 5 },
            active = true, focused = true, layer = 1)
        nodes.windowList = listOf(statusBar(), emptied)
        nodes.onClearCache = { nodes.windowList = listOf(statusBar(), newScreen()) }
        nodes.active = emptyScreen(7)
        assertEquals(listOf("2:19", "", "Nothing here yet"), readWithFix().texts())
    }

    @Test
    fun anOpenedWindowNotYetListedIsLookedForAgainAtIntervals() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        nodes.windowList = listOf(statusBar(), oldScreen())
        nodes.active = emptyScreen(7)
        now = 10
        assertEquals(listOf("2:19", "", "Empty list"), readWithFix().texts())
        assertEquals(1, nodes.cacheClears)
        now = 10 + Tree.STALE_RETRY_MS - 1
        readWithFix()
        assertEquals(1, nodes.cacheClears)
        now = 10 + Tree.STALE_RETRY_MS
        nodes.onClearCache = { nodes.windowList = listOf(statusBar(), newScreen()) }
        val r = readWithFix()
        assertEquals(2, nodes.cacheClears)
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        assertEquals("stale list", r.getJSONObject("windowFix").getString("how"))
    }

    @Test
    fun anOpenedWindowThatIsGoneIsNotLookedForAgain() {
        tree.openedWindow = { OpenedWindow(9, 0) }
        nodes.windowList = listOf(statusBar(), oldScreen())
        nodes.active = oldScreen().rootNode
        repeat(5) {
            now += Tree.STALE_RETRY_MS
            val r = readWithFix()
            assertEquals(listOf("2:19", "", "Empty list"), r.texts())
            assertFalse(r.has("windowFix"))
        }
        assertEquals(1, nodes.cacheClears)
    }

    @Test
    fun aListedOpenedWindowCostsNothing() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        nodes.windowList = listOf(statusBar(), newScreen())
        val r = readWithFix()
        assertEquals(0, nodes.cacheClears)
        assertEquals(0, nodes.activeRootReads)
        assertEquals(1, nodes.windowReads)
        assertFalse(r.has("windowFix"))
    }

    @Test
    fun aWindowJustOpenedIsNotTakenForGoneBeforeItCouldBeListed() {
        now = 5_000
        tree.openedWindow = { OpenedWindow(7, 5_000) }
        nodes.windowList = listOf(statusBar(), oldScreen())
        nodes.active = oldScreen().rootNode
        assertEquals(listOf("2:19", "", "Empty list"), readWithFix().texts())
        assertEquals(1, nodes.cacheClears)
        now = 5_000 + Tree.STALE_RETRY_MS
        nodes.onClearCache = { nodes.windowList = listOf(statusBar(), newScreen()); nodes.active = emptyScreen(7) }
        val r = readWithFix()
        assertEquals(2, nodes.cacheClears)
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        assertEquals("stale list", r.getJSONObject("windowFix").getString("how"))
    }

    @Test
    fun anActiveWindowNeverListedIsLookedForAFewTimesOnly() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        nodes.windowList = listOf(statusBar(), oldScreen())
        nodes.active = emptyScreen(7)
        repeat(10) {
            readWithFix()
            now += Tree.STALE_RETRY_MS
        }
        assertEquals(Tree.STALE_TRIES, nodes.cacheClears)
    }

    @Test
    fun withNoAppWindowListedTheOpenedWindowIsLeftToTheIssue135Recovery() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        nodes.windowList = listOf(statusBar())
        nodes.onClearCache = { nodes.windowList = listOf(statusBar(), newScreen()) }
        nodes.active = emptyScreen(7)
        val r = readWithFix()
        assertEquals(listOf("2:19", "", "Nothing here yet"), r.texts())
        assertEquals(1, nodes.cacheClears)
        assertEquals("fresh read", r.getJSONObject("windowFix").getString("how"))
    }

    @Test
    fun oneReadClearsTheCacheOnce() {
        tree.openedWindow = { OpenedWindow(7, 0) }
        nodes.windowList = listOf(statusBar(), oldScreen())
        nodes.onClearCache = { nodes.windowList = listOf(statusBar()) }
        nodes.active = emptyScreen(7)
        readWithFix()
        assertEquals(1, nodes.cacheClears)
    }
}
