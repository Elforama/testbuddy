package dev.testbuddy.agent

import android.view.MotionEvent
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityWindowInfo
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OpsTest {
    private val clock = FakeClock(1_000)
    private val probe = FakeProbe()
    private val events = Events(probe, clock)
    private val nodes = FakeNodes()
    private val tree = Tree(nodes) { clock.now }
    private val injector = FakeInjector()
    private val input = Input(injector, clock) { nodes.field }
    private val device = FakeDevice()
    private val ops = Ops(device, tree, input, events, clock)

    private fun call(req: String): JSONObject = JSONObject(ops.handle(req)!!.text)

    @Test
    fun aTreeReadWhoseWindowsVanishPastTheCacheIsNotAnError() {
        val statusBar = FakeWindow(2, AccessibilityWindowInfo.TYPE_SYSTEM,
            FakeNode("android.widget.TextView", "2:19").apply { packageName = "com.android.systemui"; windowId = 2 },
            Box(0, 0, 1080, 63), layer = 5)
        nodes.windowList = listOf(statusBar)
        nodes.active = FakeNode("android.widget.TextView", "Old orientation", bounds = Box(0, 0, 1080, 2400))
            .apply { windowId = 5 }
        nodes.onClearCache = { nodes.windowList = emptyList() }
        val reply = call("""{"id":1,"op":"tree"}""")
        assertTrue(reply.toString(), reply.getBoolean("ok"))
        assertEquals(1, nodes.cacheClears)
        val fix = reply.optJSONObject("windowFix")
        assertTrue(reply.toString(), fix != null && fix.optBoolean("pending"))
        assertEquals("pending", fix!!.getString("how"))
        nodes.onClearCache = {}
        nodes.windowList = listOf(statusBar, FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode("android.widget.TextView", "restore_top").apply { windowId = 7 },
            active = true, focused = true, layer = 1))
        val next = call("""{"id":2,"op":"tree"}""")
        assertTrue(next.toString(), next.getBoolean("ok"))
        assertTrue(next.getJSONArray("windows").toString().contains("restore_top"))
        assertFalse(next.toString(), next.optJSONObject("windowFix")?.optBoolean("pending") ?: false)
    }

    @Test
    fun aTreeReadWhoseActiveRootThrowsIsNotAnError() {
        nodes.windowList = emptyList()
        nodes.activeError = IllegalStateException("window gone")
        val reply = call("""{"id":1,"op":"tree"}""")
        assertTrue(reply.toString(), reply.getBoolean("ok"))
        assertEquals(0, reply.getJSONArray("windows").length())
        assertTrue(reply.toString(), reply.getJSONObject("windowFix").getBoolean("pending"))
    }

    @Test
    fun aWindowThatVanishesWhileWrittenIsLeftOut() {
        val app = FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode("android.widget.TextView", "restore_top").apply { windowId = 7 }, active = true, focused = true, layer = 1)
        val gone = object : UiWindow by FakeWindow(9, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode("android.widget.TextView", "Old")) {
            override val bounds: Box get() = throw IllegalStateException("window gone")
        }
        nodes.windowList = listOf(gone, app)
        val reply = call("""{"id":1,"op":"tree"}""")
        assertTrue(reply.toString(), reply.getBoolean("ok"))
        val windows = reply.getJSONArray("windows")
        assertEquals(1, windows.length())
        assertEquals(7, windows.getJSONObject(0).getInt("id"))
    }

    @Test
    fun editorPreparationDoesNotInjectInputOrSerializeAScreen() {
        val target = EditorTarget("example.app", 7, 42, "field", "EditText")
        nodes.field = object : Field by FakeField(value = "hello") {
            override fun editorTarget(): EditorTarget = target
        }
        val reply = call("""{"id":1,"op":"editorTarget"}""")
        assertTrue(reply.getBoolean("ok"))
        val descriptor = reply.getJSONObject("target")
        assertEquals("example.app", descriptor.getString("pkg"))
        assertEquals(42, descriptor.getInt("node"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(0, nodes.windowReads)
        assertEquals(1000L, clock.now)
        nodes.field = FakeField()
        assertFalse(call("""{"id":2,"op":"editorTarget"}""").has("target"))
    }

    @Test
    fun hardwareBackspaceFallbackRejectsAChangedEditor() {
        val target = EditorTarget("example.app", 7, 43, "other", "EditText")
        nodes.field = object : Field by FakeField(value = "hello") {
            override fun editorTarget(): EditorTarget = target
        }
        val original = """{"pkg":"example.app","window":7,"node":42,"id":"field","cls":"EditText"}"""
        val reply = call("""{"id":1,"op":"key","code":67,"settle":false,"expectedEditor":$original}""")
        assertFalse(reply.getBoolean("ok"))
        assertTrue(injector.keys.isEmpty())
    }

    @Test
    fun hiddenMountedNodesAreOnlyIncludedWhenRequested() {
        val hidden = FakeNode(visibleToUser = false,
            children = listOf(FakeNode(text = "Hidden child", visibleToUser = false))).apply { viewId = "closing" }
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode(children = listOf(hidden))))
        val normal = call("""{"id":1,"op":"tree"}""")
            .getJSONArray("windows").getJSONObject(0).getJSONArray("nodes")
        val diagnostic = call("""{"id":2,"op":"tree","includeHidden":true,"fresh":true}""")
            .getJSONArray("windows").getJSONObject(0).getJSONArray("nodes")
        assertEquals(1, normal.length())
        assertEquals(3, diagnostic.length())
        assertEquals("closing", diagnostic.getJSONObject(1).getString("id"))
        assertEquals(0, diagnostic.getJSONObject(1).getInt("f") and Tree.Flag.VISIBLE)
    }

    @Test
    fun closingOverlayWaitUsesRemovalCallbackEvenWhenInvisible() {
        val cover = FakeNode(visibleToUser = false).apply { viewId = "closing" }
        val root = FakeNode(children = listOf(cover))
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, root))
        clock.postDelayed({
            root.children = emptyList()
            events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
        }, 80)
        val r = call("""{"id":1,"op":"awaitCoverGone","window":1,"pkg":"com.app","nodeId":"closing","class":"android.view.View","x":50,"y":50,"timeout":1000}""")
        assertTrue(r.getBoolean("ok"))
        assertTrue(r.getBoolean("observed"))
        assertTrue(r.getBoolean("gone"))
        assertEquals(1080L, clock.now)
        assertEquals(2, nodes.windowReads)
    }

    @Test
    fun screenAndKeyboardUseOneWindowObservation() {
        nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()))
        val r = call("""{"id":1,"op":"tree"}""")
        assertTrue(r.getBoolean("keyboard"))
        assertEquals(1, nodes.windowReads)
    }

    @Test
    fun screenReadsPrefetchChildBatchesByDefault() {
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode()))
        call("""{"id":1,"op":"tree"}""")
        assertEquals(listOf(true), nodes.prefetchRequests)
    }

    @Test
    fun profilingAndPrefetchPreserveTheFullSnapshot() {
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode(children = listOf(FakeNode("android.widget.Button", "Target")))))
        val normal = call("""{"id":1,"op":"tree","prefetchChildren":false}""")
        val measured = call("""{"id":2,"op":"tree","profile":true,"prefetchChildren":true,"fresh":true}""")
        assertEquals(normal.getJSONArray("windows").toString(), measured.getJSONArray("windows").toString())
        assertFalse(normal.has("profile"))
        val p = measured.getJSONObject("profile")
        assertEquals(1, p.getInt("windowsCalls"))
        assertEquals(1, p.getInt("rootsCalls"))
        assertEquals(1, p.getInt("childrenCalls"))
        assertEquals(1, p.getInt("cacheCalls"))
        assertEquals(2, p.getInt("nodes"))
        assertEquals(listOf(false, true), nodes.prefetchRequests)
    }

    @Test
    fun unobservableFocusedFieldReportsFallbackWithoutInjecting() {
        val r = call("""{"id":1,"op":"type","text":"abc","verifyFirst":true,"ci":true}""")
        assertTrue(r.getBoolean("ok"))
        assertTrue(r.getBoolean("verifyUnavailable"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(1000L + Input.FOCUS_WAIT_MS, clock.now)
    }

    @Test
    fun unobservableFocusedFieldOutsideCIReportsFallbackAtOnce() {
        val r = call("""{"id":1,"op":"type","text":"abc","verifyFirst":true}""")
        assertTrue(r.getBoolean("ok"))
        assertTrue(r.getBoolean("verifyUnavailable"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(1000L, clock.now)
    }

    @Test
    fun checkedTypingRetriesALostFirstKeyBeforeTheRest() {
        val field = FakeField()
        nodes.field = field
        var dropped = false
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP) {
                if (!dropped) dropped = true
                else field.value += ('a' + key.code - android.view.KeyEvent.KEYCODE_A)
            }
        }
        clock.repeat(1100, 100, 5000) {
            events.onEvent(UiEvent(android.view.accessibility.AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
        }
        val r = call("""{"id":1,"op":"type","text":"abc","verifyFirst":true,"settle":false}""")
        assertTrue(r.getBoolean("ok"))
        assertEquals("abc", field.value)
        assertEquals(2300L, clock.now)
    }

    @Test
    fun checkedTypingWaitsForTheFieldCallbackWithoutReadingTheScreen() {
        val field = FakeField()
        nodes.field = field
        val at = ArrayList<Long>()
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP) {
                at += clock.now
                clock.postDelayed({
                    field.value += ('a' + key.code - android.view.KeyEvent.KEYCODE_A)
                    events.onEvent(UiEvent(AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED, "com.app", null))
                }, 80)
            }
        }
        assertTrue(call("""{"id":1,"op":"type","text":"abc","verifyFirst":true,"settle":false}""").getBoolean("ok"))
        assertEquals(listOf(1000L, 1080L, 1080L), at)
    }

    private fun appReactsToTaps() {
        injector.onTouch = { t ->
            if (t.action == MotionEvent.ACTION_UP) clock.postDelayed({
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
            }, 30)
        }
    }

    private val appWindow = FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
    private fun showWindows(vararg ws: FakeWindow, addedAt: Long = clock.now) {
        nodes.windowList = ws.toList()
        probe.windows = ws.map { it.id }.toIntArray()
        probe.boxes = ws.associate { it.id to it.bounds }
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "com.app", null, eventTime = addedAt))
    }
    private fun tap(ci: Boolean = false) =
        call("""{"id":9,"op":"tap","x":100,"y":100,"settle":false${if (ci) ",\"ci\":true" else ""}}""")

    @Test
    fun tapIntoAWindowJustAddedWaitsUntilItTakesInput() {
        for ((age, ci) in listOf(0L to false, Ops.CI_INPUT_WINDOW_AGE_MS to true)) {
            injector.touches.clear()
            clock.advance(10_000)
            showWindows()
            val added = clock.now
            showWindows(appWindow)
            injector.accept = { _, sync -> !sync || clock.now - added >= 80 }
            clock.advance(50)
            val r = tap(ci)
            if (age == 0L) {
                assertEquals("up", r.getString("notInjected"))
                assertFalse(r.has("windowWaitMs"))
                continue
            }
            assertFalse(r.has("notInjected"))
            assertEquals(age - 50, r.getLong("windowWaitMs"))
            assertEquals(added + age, injector.touches[0].event.downAt)
        }
    }

    @Test
    fun windowAgeRunsFromTheEventsTime() {
        clock.advance(10_000)
        showWindows(appWindow, addedAt = clock.now - 60)
        assertEquals(Ops.CI_INPUT_WINDOW_AGE_MS - 60, tap(ci = true).getLong("windowWaitMs"))
    }

    @Test
    fun aTapOutsideCIIsNotHeld() {
        clock.advance(10_000)
        showWindows(appWindow)
        clock.advance(10)
        val reads = nodes.windowReads
        val at = clock.now
        val r = tap()
        assertFalse(r.has("windowWaitMs"))
        assertEquals(at, injector.touches[0].event.downAt)
        assertEquals(reads + 1, nodes.windowReads)
    }

    private fun aimedTap(read: Long, ref: Int) =
        call("""{"id":9,"op":"tap","x":50,"y":50,"settle":false,"aim":{"read":$read,"window":1,"ref":$ref}}""")

    @Test
    fun anAimedTapSaysWhereItsNodeWasJustBeforeTheTouch() {
        val toggle = FakeNode(text = "Menu", bounds = Box(10, 10, 100, 100))
        val other = FakeNode(bounds = Box(0, 200, 1080, 300))
        showWindows(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(children = listOf(toggle, other))))
        val read = call("""{"id":1,"op":"tree"}""").getLong("read")
        assertTrue(read > 0)
        toggle.appBounds = Box(310, 10, 400, 100)
        var touchesAtRefresh = -1
        toggle.onRefresh = { touchesAtRefresh = injector.touches.size }
        val r = aimedTap(read, 2)
        assertEquals(0, touchesAtRefresh)
        val at = r.getJSONArray("aimAt")
        assertEquals(listOf(310, 10, 400, 100), (0 until 4).map { at.getInt(it) })
        assertTrue(r.has("aimMs"))

        assertFalse(aimedTap(read + 1000, 2).has("aimAt"))
        assertFalse(aimedTap(read, 9).has("aimAt"))
        toggle.appShown = false
        assertFalse(aimedTap(read, 2).has("aimAt"))
        toggle.appShown = true
        toggle.gone = true
        assertFalse(aimedTap(read, 2).has("aimAt"))

        val refreshes = toggle.refreshes
        assertFalse(tap().has("aimAt"))
        assertEquals(refreshes, toggle.refreshes)
        repeat(Tree.KEPT_READS) { call("""{"id":1,"op":"tree"}""") }
        toggle.gone = false
        assertFalse(aimedTap(read, 2).has("aimAt"))
    }

    @Test
    fun aReboundRowOrABareContainerSaysNothing() {
        val row = FakeNode(text = "Alice", bounds = Box(0, 500, 1080, 600)).apply { clickable = true }
        val root = FakeNode(children = listOf(row))
        showWindows(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, root))
        val read = call("""{"id":1,"op":"tree"}""").getLong("read")
        row.onRefresh = { row.text = "Bob" }
        val r = call("""{"id":9,"op":"tap","x":50,"y":550,"settle":false,"aim":{"read":$read,"window":1,"ref":2}}""")
        assertEquals(1, row.refreshes)
        assertFalse(r.has("aimAt"))
        row.onRefresh = {}
        val read2 = call("""{"id":1,"op":"tree"}""").getLong("read")
        assertTrue(call("""{"id":9,"op":"tap","x":50,"y":550,"settle":false,"aim":{"read":$read2,"window":1,"ref":2}}""").has("aimAt"))
        assertFalse(call("""{"id":9,"op":"tap","x":50,"y":550,"settle":false,"aim":{"read":$read2,"window":1,"ref":1}}""").has("aimAt"))
    }

    @Test
    fun aSlowRefreshDoesNotHoldTheTouch() {
        val toggle = FakeNode(text = "Menu", bounds = Box(10, 10, 100, 100))
        showWindows(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(children = listOf(toggle))))
        val read = call("""{"id":1,"op":"tree"}""").getLong("read")
        val stuck = java.util.concurrent.CountDownLatch(1)
        toggle.onRefresh = { stuck.await() }
        val t0 = System.nanoTime()
        val r = aimedTap(read, 2)
        val tookMs = (System.nanoTime() - t0) / 1_000_000
        assertFalse(r.has("aimAt"))
        assertEquals(1, injector.touches.count { it.event.action == MotionEvent.ACTION_DOWN })
        assertTrue("took $tookMs ms", tookMs < Tree.REFRESH_BUDGET_MS + 400)
        assertFalse(aimedTap(read, 2).has("aimAt"))
        assertEquals(1, toggle.refreshes)
        stuck.countDown()
        toggle.onRefresh = {}
        var again = false
        val until = System.nanoTime() + 2_000_000_000L
        while (!again && System.nanoTime() < until) again = aimedTap(read, 2).has("aimAt")
        assertTrue(again)
    }

    @Test
    fun firstInWindowOnlyForTheFirstTouchIntoAWindow() {
        clock.advance(10_000)
        showWindows()
        showWindows(appWindow)
        clock.advance(150)
        val r = tap()
        assertTrue(r.getBoolean("firstInWindow"))
        assertEquals(150L, r.getLong("windowAgeMs"))
        clock.advance(100)
        val again = tap()
        assertFalse(again.getBoolean("firstInWindow"))
        assertEquals(250L, again.getLong("windowAgeMs"))
    }

    @Test
    fun theAppsWindowBackAfterAModalClosedIsNotNew() {
        clock.advance(10_000)
        showWindows()
        showWindows(appWindow)
        clock.advance(200)
        assertTrue(tap().getBoolean("firstInWindow"))
        val dialog = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 1080, 1400))
        showWindows(dialog, appWindow)
        clock.advance(150)
        val onDialog = tap()
        assertTrue(onDialog.getBoolean("firstInWindow"))
        assertEquals(150L, onDialog.getLong("windowAgeMs"))
        showWindows(appWindow)
        clock.advance(100)
        assertFalse(tap().getBoolean("firstInWindow"))
        val reopened = FakeWindow(6, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 1080, 1400))
        showWindows(reopened, appWindow)
        clock.advance(100)
        assertTrue(tap().getBoolean("firstInWindow"))
        val fullScreen = FakeWindow(7, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
        showWindows(fullScreen)
        clock.advance(100)
        assertTrue(tap().getBoolean("firstInWindow"))
        showWindows(appWindow)
        clock.advance(100)
        val back = tap()
        assertFalse(back.getBoolean("firstInWindow"))
        assertEquals(750L, back.getLong("windowAgeMs"))
    }

    private fun opened(w: FakeWindow, cls: String, pkg: String = "com.app", activity: Boolean = false) {
        if (activity) device.activities += "$pkg/$cls"
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, pkg, cls, windowId = w.id))
    }

    @Test
    fun aViewsEventDoesNotMakeAnActivityADialog() {
        for ((cls, late) in listOf("androidx.drawerlayout.widget.DrawerLayout" to 0L, "com.app.SidePane" to 1_200L)) {
            clock.advance(10_000)
            showWindows()
            val home = FakeWindow(20, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
            showWindows(home)
            opened(home, "com.app.MainActivity", activity = true)
            clock.advance(2_000)
            val next = FakeWindow(21 + late.toInt() / 1000, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
            showWindows(next)
            clock.advance(late)
            opened(next, cls)
            clock.advance(100)
            val r = tap()
            assertTrue(r.getBoolean("firstInWindow"))
            assertFalse("$cls: windowOver", r.getBoolean("windowOver"))
        }
    }

    @Test
    fun onlyAWindowOverTheAppComesIn() {
        clock.advance(10_000)
        showWindows()
        showWindows(appWindow)
        opened(appWindow, "com.app.MainActivity", activity = true)
        clock.advance(100)
        val activity = tap()
        assertTrue(activity.getBoolean("firstInWindow"))
        assertFalse(activity.getBoolean("windowOver"))
        val dialog = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(40, 40, 1040, 1400))
        showWindows(dialog)
        opened(dialog, "androidx.compose.ui.window.DialogWrapper")
        clock.advance(100)
        assertTrue(tap().getBoolean("windowOver"))
        showWindows(appWindow)
        val other = FakeWindow(6, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = "com.android.permissioncontroller" })
        showWindows(other)
        opened(other, "com.android.permissioncontroller.GrantPermissionsActivity", "com.android.permissioncontroller", activity = true)
        clock.advance(100)
        val permission = tap()
        assertTrue(permission.getBoolean("firstInWindow"))
        assertFalse(permission.getBoolean("windowOver"))
        val unseen = FakeWindow(8, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(40, 40, 1040, 1400))
        showWindows(unseen, appWindow)
        clock.advance(100)
        assertFalse(tap().getBoolean("windowOver"))
    }

    @Test
    fun anotherPackagesActivityFloatingOverTheAppComesOver() {
        val perm = "com.google.android.permissioncontroller"
        val grant = "com.android.permissioncontroller.permission.ui.GrantPermissionsActivity"
        val dialogBox = Box(28, 742, 1052, 1730)
        fun tapDialog() = call("""{"id":9,"op":"tap","x":540,"y":1237,"settle":false}""")
        fun appWithInput(id: Int): FakeWindow {
            clock.advance(10_000)
            showWindows()
            val app = FakeWindow(id, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
            showWindows(app)
            opened(app, "com.app.MainActivity", activity = true)
            clock.advance(2_000)
            tapDialog()
            return app
        }

        appWithInput(30)
        val dialog = FakeWindow(31, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = perm }, dialogBox)
        showWindows(dialog)
        opened(dialog, grant, perm, activity = true)
        clock.advance(50)
        opened(dialog, grant, perm, activity = true)
        clock.advance(50)
        val onDialog = tapDialog()
        assertTrue(onDialog.getBoolean("firstInWindow"))
        assertTrue("the permission dialog over the app", onDialog.getBoolean("windowOver"))

        appWithInput(40)
        val own = FakeWindow(41, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), dialogBox)
        showWindows(own)
        opened(own, "com.app.ConfirmActivity", activity = true)
        clock.advance(100)
        assertFalse("the app's own Activity", tapDialog().getBoolean("windowOver"))

        appWithInput(50)
        val browser = FakeWindow(51, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = "com.browser" })
        showWindows(browser)
        opened(browser, "com.browser.TabActivity", "com.browser", activity = true)
        clock.advance(100)
        assertFalse("another app over all of the app", tapDialog().getBoolean("windowOver"))

        clock.advance(10_000)
        showWindows()
        val left = FakeWindow(60, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 538, 2400))
        showWindows(left)
        opened(left, "com.app.MainActivity", activity = true)
        clock.advance(2_000)
        call("""{"id":9,"op":"tap","x":200,"y":1237,"settle":false}""")
        val beside = FakeWindow(61, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = perm }, Box(600, 742, 1052, 1730))
        showWindows(beside, left)
        opened(beside, grant, perm, activity = true)
        clock.advance(100)
        val besideTap = call("""{"id":9,"op":"tap","x":800,"y":1237,"settle":false}""")
        assertTrue(besideTap.getBoolean("firstInWindow"))
        assertFalse("beside the app", besideTap.getBoolean("windowOver"))

        appWithInput(70)
        val app70 = FakeWindow(70, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
        val cascaded = FakeWindow(71, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = "com.browser" }, dialogBox)
        showWindows(cascaded, app70)
        opened(cascaded, "com.browser.TabActivity", "com.browser", activity = true)
        clock.advance(100)
        val freeform = tapDialog()
        assertTrue(freeform.getBoolean("firstInWindow"))
        assertFalse("freeform: the app's window still listed", freeform.getBoolean("windowOver"))

        appWithInput(80)
        val later = FakeWindow(81, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = perm }, dialogBox)
        showWindows(later)
        opened(later, grant, perm, activity = true)
        clock.advance(50)
        opened(later, "com.android.permissioncontroller.permission.ui.ManagePermissionsActivity", perm, activity = true)
        clock.advance(50)
        assertFalse("a later Activity event", tapDialog().getBoolean("windowOver"))
    }

    @Test
    fun aNewActivityInSplitScreenIsNotOver() {
        clock.advance(10_000)
        showWindows()
        val left = FakeWindow(10, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 538, 2400))
        val right = FakeWindow(11, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(542, 0, 1080, 2400))
        showWindows(right, left)
        opened(left, "com.app.ListActivity", activity = true)
        opened(right, "com.app.CartActivity", activity = true)
        clock.advance(2_000)
        val next = FakeWindow(12, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(542, 0, 1080, 2400))
        showWindows(next, left)
        opened(next, "com.app.CheckoutActivity", activity = true)
        clock.advance(100)
        val split = JSONObject(ops.handle("""{"id":9,"op":"tap","x":800,"y":100,"settle":false}""")!!.text)
        assertTrue(split.getBoolean("firstInWindow"))
        assertFalse(split.getBoolean("windowOver"))
    }

    @Test
    fun aLandscapeActivityNarrowerThanTheDisplayIsNotOver() {
        clock.advance(10_000)
        showWindows()
        device.size = intArrayOf(2400, 1080)
        val home = FakeWindow(16, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 2268, 1080))
        showWindows(home)
        opened(home, "com.app.MainActivity", activity = true)
        clock.advance(2_000)
        val landscape = FakeWindow(13, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 2268, 1080))
        showWindows(landscape)
        opened(landscape, "com.app.PayActivity", activity = true)
        clock.advance(100)
        val wide = tap()
        assertTrue(wide.getBoolean("firstInWindow"))
        assertFalse(wide.getBoolean("windowOver"))
    }

    @Test
    fun aFullScreenSheetOverItsActivityIsOver() {
        clock.advance(10_000)
        showWindows()
        val activity = FakeWindow(14, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
        showWindows(activity)
        opened(activity, "com.app.MainActivity", activity = true)
        clock.advance(2_000)
        val sheet = FakeWindow(15, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), layer = 1)
        showWindows(sheet, activity)
        opened(sheet, "com.google.android.material.bottomsheet.BottomSheetDialog")
        clock.advance(100)
        val onSheet = tap()
        assertTrue(onSheet.getBoolean("firstInWindow"))
        assertTrue(onSheet.getBoolean("windowOver"))
    }

    @Test
    fun aSwipeIsTheWindowsInput() {
        clock.advance(10_000)
        showWindows()
        showWindows(appWindow)
        clock.advance(100)
        call("""{"id":1,"op":"swipe","x1":100,"y1":100,"x2":100,"y2":50,"settle":false}""")
        assertFalse(tap().getBoolean("firstInWindow"))
    }

    @Test
    fun aWindowHiddenWhenTheAgentStartedIsOld() {
        clock.advance(10_000)
        probe.windows = intArrayOf(1, 5)
        val restarted = Events(probe, clock)
        val ops2 = Ops(device, tree, input, restarted, clock)
        val hidden = FakeWindow(3, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
        nodes.windowList = listOf(hidden)
        probe.windows = intArrayOf(1, 3)
        restarted.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "com.app", null, eventTime = clock.now))
        clock.advance(100)
        val r = JSONObject(ops2.handle("""{"id":9,"op":"tap","x":100,"y":100,"settle":false}""")!!.text)
        assertFalse(r.has("firstInWindow"))
    }

    @Test
    fun aKeyIsTheWindowsFirstInput() {
        clock.advance(10_000)
        showWindows()
        showWindows(appWindow)
        probe.activeWindow = appWindow.id
        clock.advance(100)
        call("""{"id":3,"op":"key","code":66,"settle":false}""")
        assertFalse(tap().getBoolean("firstInWindow"))
    }

    @Test
    fun oldWindowsSayNothingOfFirstInput() {
        clock.advance(10_000)
        probe.windows = intArrayOf(appWindow.id)
        nodes.windowList = listOf(appWindow)
        val started = Ops(device, tree, input, Events(probe, clock), clock)
        clock.advance(100)
        var reads = nodes.windowReads
        val before = JSONObject(started.handle("""{"id":9,"op":"tap","x":100,"y":100,"settle":false}""")!!.text)
        assertFalse(before.has("firstInWindow"))
        assertEquals(reads, nodes.windowReads)
        showWindows()
        showWindows(appWindow)
        clock.advance(5_000)
        reads = nodes.windowReads
        val r = tap()
        assertFalse(r.has("firstInWindow"))
        assertFalse(r.has("windowAgeMs"))
        assertEquals(reads, nodes.windowReads)
    }

    @Test
    fun tapIntoAnOldWindowDoesNotWait() {
        showWindows(appWindow)
        clock.advance(5_000)
        val reads = nodes.windowReads
        val at = clock.now
        val r = tap(ci = true)
        assertFalse(r.has("windowWaitMs"))
        assertEquals(at, injector.touches[0].event.downAt)
        assertEquals(reads, nodes.windowReads)
    }

    @Test
    fun otherWindowsChangingDoNotDelayATap() {
        showWindows(appWindow)
        clock.advance(5_000)
        val ime = FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode(), Box(0, 1600, 1080, 2400))
        val bar = FakeWindow(4, AccessibilityWindowInfo.TYPE_SYSTEM, FakeNode(), Box(0, 0, 1080, 60))
        val dialog = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(100, 900, 980, 1400))
        val changes = listOf(
            { showWindows(ime, appWindow) },
            { showWindows(bar, ime, appWindow) },
            { showWindows(dialog, appWindow) },
            { showWindows(appWindow) },
            { showWindows(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 1080, 2000), title = "Other")) },
        )
        for (change in changes) {
            change()
            clock.advance(10)
            injector.touches.clear()
            val at = clock.now
            val r = tap(ci = true)
            assertFalse(r.has("windowWaitMs"))
            assertEquals(at, injector.touches[0].event.downAt)
        }
    }

    @Test
    fun aTouchNotInjectedIsReported() {
        injector.accept = { t, _ -> t.action != MotionEvent.ACTION_DOWN }
        var r = tap()
        assertEquals("down", r.getString("notInjected"))
        assertEquals(1, injector.touches.size)
        injector.touches.clear()
        injector.accept = { _, sync -> !sync }
        r = tap()
        assertEquals("up", r.getString("notInjected"))
        assertEquals(listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL),
            injector.touches.map { it.event.action })
        injector.accept = { _, _ -> true }
        assertFalse(tap().has("notInjected"))
    }

    @Test
    fun hidingKeyboardWithoutAnImeNeverSendsBack() {
        val r = call("""{"id":1,"op":"hideKeyboard"}""")
        assertTrue(r.getBoolean("ok"))
        assertTrue(r.getBoolean("hidden"))
        assertTrue(injector.keys.isEmpty())
    }

    @Test
    fun hidingKeyboardWaitsOnItsWindowCallback() {
        nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()))
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP) clock.postDelayed({
                nodes.windowList = emptyList()
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
            }, 80)
        }
        val r = call("""{"id":1,"op":"hideKeyboard","timeout":500}""")
        assertTrue(r.getBoolean("ok"))
        assertTrue(r.getBoolean("hidden"))
        assertEquals(listOf(4,4), injector.keys.map { it.event.code })
        assertEquals(1080L, clock.now)
    }

    @Test
    fun keyboardThatStaysUpFailsWithoutAnotherBack() {
        nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()))
        val r = call("""{"id":1,"op":"hideKeyboard","timeout":500}""")
        assertTrue(r.getBoolean("ok"))
        assertFalse(r.getBoolean("hidden"))
        assertEquals(2, injector.keys.size)
        assertEquals(1500L,clock.now)
    }

    @Test
    fun hidingKeyboardClosesOneStillComingUp() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.postDelayed({
            nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()))
            events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
        }, 600)
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP) clock.postDelayed({
                nodes.windowList = emptyList()
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
            }, 80)
        }
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertTrue(r.getBoolean("late"))
        assertEquals(listOf(4, 4), injector.keys.map { it.event.code })
        assertEquals(1780L, clock.now)
    }

    @Test
    fun hidingKeyboardClosesOneANewScreenBringsUp() {
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        for (at in listOf(700L, 1400L)) clock.postDelayed({
            events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.app", null))
        }, at)
        clock.postDelayed({
            nodes.field = FakeField()
            nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()))
            events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
        }, 1900)
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP) clock.postDelayed({
                nodes.windowList = emptyList()
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
            }, 80)
        }
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertEquals(listOf(4, 4), injector.keys.map { it.event.code })
        assertEquals(3080L, clock.now)
    }

    @Test
    fun hidingKeyboardWaitOnAChangingScreenIsBounded() {
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.repeat(1100, 100, 10_000) {
            events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
        }
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(3000L, clock.now)
    }

    @Test
    fun hidingKeyboardThatNeverComesWaitsOnlyAfterAnAction() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.advance(300)
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(2000L, clock.now)
        clock.advance(5000)
        call("""{"id":3,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(injector.keys.isEmpty())
        assertEquals(7000L, clock.now)
    }

    private fun imeUp() {
        nodes.windowList = listOf(FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()))
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
    }

    private fun imeDown() {
        nodes.windowList = emptyList()
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "ime", null))
    }

    @Test
    fun hidingKeyboardNeverSeenSinceTheTapWaitsAfterTyping() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.advance(200)
        call("""{"id":2,"op":"type","text":"ap","settle":false}""")
        val typedAt = clock.now
        call("""{"id":3,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertEquals(typedAt + 1000, clock.now)
        assertFalse(injector.keys.any { it.event.code == android.view.KeyEvent.KEYCODE_BACK })
    }

    @Test
    fun hidingKeyboardClosesTheAutofocusKeyboardThatShowsAfterTyping() {
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.advance(150)
        nodes.field = FakeField()
        call("""{"id":2,"op":"type","text":"auto","settle":false}""")
        clock.postDelayed({ imeUp() }, 100)
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP && key.code == android.view.KeyEvent.KEYCODE_BACK) {
                clock.postDelayed({ imeDown() }, 80)
            }
        }
        val r = call("""{"id":3,"op":"hideKeyboard","timeout":500,"pendingMs":2500}""")
        assertTrue(r.getBoolean("hidden"))
        assertTrue(r.getBoolean("late"))
        assertEquals(listOf(4, 4), injector.keys.filter { it.event.code == 4 }.map { it.event.code })
        assertFalse(tree.keyboardShown())
    }

    @Test
    fun hidingKeyboardAfterEnterDoesNotWait() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        imeUp()
        call("""{"id":2,"op":"info"}""")
        injector.onKey = { key -> if (key.action == android.view.KeyEvent.ACTION_UP) imeDown() }
        call("""{"id":3,"op":"key","code":66,"settle":false}""")
        val t = clock.now
        val r = call("""{"id":4,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertEquals(t, clock.now)
        assertEquals(listOf(66, 66), injector.keys.map { it.event.code })
    }

    @Test
    fun hidingKeyboardTwiceDoesNotWait() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        imeUp()
        injector.onKey = { key ->
            if (key.action == android.view.KeyEvent.ACTION_UP) clock.postDelayed({ imeDown() }, 80)
        }
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertFalse(r.has("late"))
        assertEquals(1080L, clock.now)
        call("""{"id":3,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertEquals(1080L, clock.now)
        assertEquals(listOf(4, 4), injector.keys.map { it.event.code })
    }

    @Test
    fun hidingKeyboardWithoutPendingMsDoesNotWait() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        call("""{"id":2,"op":"hideKeyboard","timeout":500}""")
        assertEquals(1000L, clock.now)
    }

    @Test
    fun hidingKeyboardSendsNoBackForAFlicker() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.postDelayed({ imeUp() }, 300)
        clock.postDelayed({ imeDown() }, 340)
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(2340L, clock.now)
    }

    @Test
    fun helloSaysWhatTheDeviceIs() {
        val h = JSONObject(ops.hello())
        assertEquals("hello", h.getString("ev"))
        assertEquals("android", h.getString("platform"))
        assertEquals(AGENT_VERSION, h.getString("version"))
        assertEquals(36, h.getInt("sdk"))
        assertEquals(1080, h.getInt("w"))
        assertEquals(2400, h.getInt("h"))
        assertEquals("fake", h.getString("inject"))
    }

    @Test
    fun linesThatAreNoRequestGetNoReply() {
        assertNull(ops.handle(""))
        assertNull(ops.handle("   "))
        assertNull(ops.handle("not json"))
    }

    @Test
    fun anUnknownOpFails() {
        val r = call("""{"id":3,"op":"fly"}""")
        assertEquals(3, r.getInt("id"))
        assertFalse(r.getBoolean("ok"))
        assertEquals("unknown op \"fly\"", r.getString("error"))
    }

    @Test
    fun aTapWaitsForTheAppToSettle() {
        appReactsToTaps()
        val r = call("""{"id":1,"op":"tap","x":10,"y":20}""")
        assertTrue(r.getBoolean("ok"))
        assertEquals("changed", r.getString("result"))
        assertEquals(1, r.getInt("seq"))
        assertEquals(30 + Events.DEFAULT_QUIET_MS, r.getInt("settleMs"))
    }

    @Test
    fun aTapSaysWhetherAViewTookTheClick() {
        appReactsToTaps()
        assertFalse(call("""{"id":1,"op":"tap","x":10,"y":20}""").getBoolean("clicked"))
        injector.onTouch = { t ->
            if (t.action == MotionEvent.ACTION_UP) clock.postDelayed({
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_VIEW_CLICKED, "com.app", "android.widget.Button"))
            }, 30)
        }
        assertTrue(call("""{"id":2,"op":"tap","x":10,"y":20}""").getBoolean("clicked"))
    }

    @Test
    fun aClickReportedAfterTheReplyCountsLater() {
        val r = call("""{"id":1,"op":"tap","x":10,"y":20,"settle":false}""")
        assertFalse(r.getBoolean("clicked"))
        val before = r.getLong("clicksBefore")
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_VIEW_CLICKED, "com.app", "android.widget.Button"))
        assertEquals(before + 1, call("""{"id":2,"op":"clicks"}""").getLong("clicks"))
    }

    @Test
    fun aTapNothingReactsToIsNoChange() {
        val r = call("""{"id":1,"op":"tap","x":10,"y":20,"noChange":200}""")
        assertEquals("no_change", r.getString("result"))
        assertEquals(200, r.getInt("settleMs"))
    }

    @Test
    fun anActionWithoutSettleReturnsAtOnce() {
        appReactsToTaps()
        val r = call("""{"id":1,"op":"tap","x":10,"y":20,"settle":false}""")
        assertFalse(r.has("result"))
        assertEquals(1_000L, clock.now)
    }

    @Test
    fun aDragIsADragAndASwipeIsASwipe() {
        call("""{"id":1,"op":"swipe","x1":500,"y1":1500,"x2":500,"y2":500,"drag":true,"settle":false}""")
        assertEquals(MotionEvent.ACTION_CANCEL, injector.touches.last().event.action)
        injector.touches.clear()
        call("""{"id":2,"op":"swipe","x1":500,"y1":1500,"x2":500,"y2":500,"ms":160,"settle":false}""")
        assertEquals(MotionEvent.ACTION_UP, injector.touches.last().event.action)
        assertEquals(10, injector.touches.count { it.event.action == MotionEvent.ACTION_MOVE })
    }

    @Test
    fun aSwipeWithAHoldIsAPressAndHoldDragNeverSentTwice() {
        val t0 = clock.now
        val req = """{"id":1,"op":"swipe","x1":500,"y1":1500,"x2":500,"y2":500,"ms":400,"hold":800,"drag":true,"settle":false,"rid":"d-1"}"""
        val first = ops.handle(req)!!.text
        val events = injector.touches.map { it.event }
        assertEquals(MotionEvent.ACTION_DOWN, events.first().action)
        assertEquals(MotionEvent.ACTION_UP, events.last().action)
        assertEquals("still for the hold", 800L + Input.FRAME_MS, events[1].at - events[0].at)
        assertEquals("hold, move, rest", 800L + 400L + Input.HOLD_DRAG_DROP_MS, events.last().at - events.first().at)
        assertTrue(clock.now - t0 >= 1500)
        val n = injector.touches.size
        val again = ops.handle(req.replace("\"id\":1", "\"id\":2"))!!.text
        assertEquals("dragged once", n, injector.touches.size)
        assertEquals(Ops.withId(first, 2), again)
    }

    @Test
    fun aRequestSentAgainIsAnsweredFromItsFirstRun() {
        appReactsToTaps()
        val first = ops.handle("""{"id":1,"op":"tap","x":10,"y":20,"rid":"r-1"}""")!!.text
        val again = ops.handle("""{"id":2,"op":"tap","x":10,"y":20,"rid":"r-1"}""")!!.text
        assertEquals(2, injector.touches.size)
        assertEquals(Ops.withId(first, 2), again)
        assertEquals(2, JSONObject(again).getInt("id"))
        ops.handle("""{"id":3,"op":"tap","x":10,"y":20,"rid":"r-2"}""")
        assertEquals(4, injector.touches.size)
    }

    @Test
    fun onlyTheLatestRequestTokensAreKept() {
        for (i in 0..Ops.RECENT_REQUESTS) call("""{"id":$i,"op":"tap","x":1,"y":1,"settle":false,"rid":"r$i"}""")
        val taps = injector.touches.size
        call("""{"id":100,"op":"tap","x":1,"y":1,"settle":false,"rid":"r${Ops.RECENT_REQUESTS}"}""")
        assertEquals(taps, injector.touches.size)
        call("""{"id":101,"op":"tap","x":1,"y":1,"settle":false,"rid":"r0"}""")
        assertEquals(taps + 2, injector.touches.size)
    }

    @Test
    fun withIdReaddressesAReply() {
        assertEquals("{\"id\":9,\"ok\":true}", Ops.withId("{\"id\":1,\"ok\":true}", 9))
        assertEquals("{}", Ops.withId("{}", 9))
    }

    @Test
    fun typingWithNoFieldToTypeIntoFails() {
        val r = call("""{"id":1,"op":"type","text":"😀","settle":false}""")
        assertFalse(r.getBoolean("ok"))
        assertEquals("no focused text field to type into", r.getString("error"))
    }

    @Test
    fun clearSaysWhetherAFieldWasCleared() {
        nodes.field = FakeField("abc")
        assertTrue(call("""{"id":1,"op":"clear","settle":false}""").getBoolean("cleared"))
    }

    @Test
    fun aMissingArgumentFailsTheRequestOnly() {
        val r = call("""{"id":1,"op":"tap","x":10}""")
        assertFalse(r.getBoolean("ok"))
        assertTrue(r.getString("error"), r.getString("error").contains("y"))
        assertTrue(call("""{"id":2,"op":"ping"}""").getBoolean("ok"))
    }

    @Test
    fun treeWritesTheScreenAndTheKeyboard() {
        nodes.windowList = listOf(
            FakeWindow(3, AccessibilityWindowInfo.TYPE_INPUT_METHOD, FakeNode()),
            FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode("android.widget.Button", "Go")),
        )
        val r = call("""{"id":1,"op":"tree"}""")
        assertTrue(r.getBoolean("keyboard"))
        assertEquals(2, r.getJSONArray("windows").length())
        assertEquals("Go", r.getJSONArray("windows").getJSONObject(1).getJSONArray("nodes").getJSONObject(0).getString("t"))
    }

    @Test
    fun setQuietIsKeptInBounds() {
        assertEquals(50, call("""{"id":1,"op":"setQuiet","ms":1}""").getInt("quiet"))
        assertEquals(5000, call("""{"id":2,"op":"setQuiet","ms":99999}""").getInt("quiet"))
        assertEquals(300, call("""{"id":3,"op":"setQuiet","ms":300}""").getInt("quiet"))
        assertEquals(300, events.quietMs)
    }

    @Test
    fun animationsTellEventsWhetherTheyRun() {
        assertFalse(call("""{"id":1,"op":"animations","on":false}""").getBoolean("on"))
        assertTrue(events.animationsOff)
        assertTrue(call("""{"id":2,"op":"animations","on":true}""").getBoolean("on"))
        assertFalse(events.animationsOff)
    }

    @Test
    fun keepMakesTheAgentStayLonger() {
        assertEquals(Server.ORPHAN_EXIT_MS, ops.idleLimitMs)
        assertEquals(Server.IDLE_EXIT_MS, call("""{"id":1,"op":"keep"}""").getLong("idleMs"))
        assertEquals(Server.IDLE_EXIT_MS, ops.idleLimitMs)
    }

    @Test
    fun rotateNamesMapToRotations() {
        for (to in listOf("portrait", "landscape", "landscapeLeft", "landscapeRight", "upsideDown", "auto")) {
            assertTrue(call("""{"id":1,"op":"rotate","to":"$to"}""").getBoolean("ok"))
        }
        assertEquals(listOf(Rotation.PORTRAIT, Rotation.LANDSCAPE_LEFT, Rotation.LANDSCAPE_LEFT, Rotation.LANDSCAPE_RIGHT,
            Rotation.UPSIDE_DOWN, Rotation.FREE), device.rotations)
    }

    @Test
    fun infoReadsTheDisplayAsTurned() {
        device.turned = 1
        device.size = intArrayOf(2400, 1080)
        val r = call("""{"id":1,"op":"info"}""")
        assertEquals(2400, r.getInt("w"))
        assertEquals(1, r.getInt("rotation"))
        assertFalse(r.getBoolean("keyboard"))
    }

    @Test
    fun aScreenshotComesAsBinaryAfterTheReply() {
        val reply = ops.handle("""{"id":1,"op":"screenshot","format":"jpg","scale":0.5,"quality":60}""")!!
        val r = JSONObject(reply.text)
        assertEquals(10, r.getInt("bin"))
        assertEquals(10, reply.bin!!.size)
        assertEquals("jpeg", r.getString("format"))
        assertEquals(Triple(0.5, "jpeg", 60), device.shotRequest)
        assertEquals(540, r.getInt("w"))
    }

    @Test
    fun shellRunsOnTheDevice() {
        val r = call("""{"id":1,"op":"sh","cmd":"echo ok"}""")
        assertEquals(listOf("echo ok"), device.shells)
        assertEquals(0, r.getInt("code"))
        assertEquals("ok\n", r.getString("out"))
        assertFalse(r.has("timedOut"))
        device.shellResult = ShellResult(-1, "", true)
        assertTrue(call("""{"id":2,"op":"sh","cmd":"sleep 99"}""").getBoolean("timedOut"))
    }

    @Test
    fun foregroundAndAwaitPackage() {
        assertEquals("com.app", call("""{"id":1,"op":"foreground"}""").getString("pkg"))
        probe.activePkg = "com.app"
        assertTrue(call("""{"id":2,"op":"awaitPackage","pkg":"com.app"}""").getBoolean("found"))
        assertFalse(call("""{"id":3,"op":"awaitPackage","pkg":"com.other","timeout":500}""").getBoolean("found"))
    }

    @Test
    fun exitSaysByeThenExits() {
        assertTrue(call("""{"id":1,"op":"exit"}""").getBoolean("bye"))
        assertEquals(1, device.exits)
    }
}
