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

/** The protocol's requests and replies, against a fake device. */
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

    // AND-005: preparing an editor key is a read/fence, never an early deletion.
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

    // AND-004: an opacity-zero overlay can remain mounted and intercept input.
    // Readiness diagnostics must be able to observe it without exposing hidden
    // nodes in ordinary snapshots.
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
        assertEquals(1000L + Input.FOCUS_WAIT_MS, clock.now) // CI mode: waited for focus to arrive (issue #97)
    }

    /** The host asks for the focus wait only in CI mode ("ci": true); without it there is none. */
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
        // Issue #299: the key is typed again only once the app's UI has sent an event after it
        // and the field still does not show it a cap later.
        clock.repeat(1100, 100, 5000) {
            events.onEvent(UiEvent(android.view.accessibility.AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
        }
        val r = call("""{"id":1,"op":"type","text":"abc","verifyFirst":true,"settle":false}""")
        assertTrue(r.getBoolean("ok"))
        assertEquals("abc", field.value)
        assertEquals(2300L, clock.now) // the latest event at 2000, still not shown a cap (300 ms) later
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

    /** The app redraws 30 ms after a touch lifts. */
    private fun appReactsToTaps() {
        injector.onTouch = { t ->
            if (t.action == MotionEvent.ACTION_UP) clock.postDelayed({
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
            }, 30)
        }
    }

    // Issue #204: windows on screen, top-most first, and when the agent was told each was added.
    private val appWindow = FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode())
    private fun showWindows(vararg ws: FakeWindow, addedAt: Long = clock.now) {
        nodes.windowList = ws.toList()
        probe.windows = ws.map { it.id }.toIntArray()
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "com.app", null, eventTime = addedAt))
    }
    private fun tap(ci: Boolean = false) =
        call("""{"id":9,"op":"tap","x":100,"y":100,"settle":false${if (ci) ",\"ci\":true" else ""}}""")

    // Issue #204: Android drops a touch sent into a window just added whose input channel is not
    // up yet (InputDispatcher: NO_INPUT_CHANNEL). Here the dispatcher takes touches into the new
    // window only once it is 80 ms old. Without the wait (outside CI mode) the tap 50 ms after
    // the window came is dropped: the platform reports its UP not delivered. In CI mode the tap
    // is held until the window is 400 ms old, and lands. The hold is CI mode's: #204 was seen on
    // a CI emulator only (aTapOutsideCIIsNotHeld).
    @Test
    fun tapIntoAWindowJustAddedWaitsUntilItTakesInput() {
        for ((age, ci) in listOf(0L to false, Ops.CI_INPUT_WINDOW_AGE_MS to true)) {
            injector.touches.clear()
            clock.advance(10_000)
            showWindows() // the window goes, then comes again: new
            val added = clock.now
            showWindows(appWindow)
            // An async DOWN is queued either way; the dispatcher's verdict comes with the sync UP.
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

    // The age runs from when the window was added (the event's time), not from when the agent
    // heard of it: an event that reached the agent 60 ms late leaves 340 ms to wait (CI mode).
    @Test
    fun windowAgeRunsFromTheEventsTime() {
        clock.advance(10_000)
        showWindows(appWindow, addedAt = clock.now - 60)
        assertEquals(Ops.CI_INPUT_WINDOW_AGE_MS - 60, tap(ci = true).getLong("windowWaitMs"))
    }

    // Outside CI mode a tap into a window just added is sent at once, as before #204's fix. One
    // read finds the window under it, for firstInWindow (issue #280).
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

    // Issue #321: a touch's reply carries the seq of the latest solid change as it went in, for
    // the host to tell whether anything changed between the read it aimed from and the touch.
    @Test
    fun aTouchsReplySaysTheLatestSolidChangeAsItWentIn() {
        val first = tap().getLong("solidAtTouch")
        assertEquals(events.solidSeq, first)
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.app", "android.app.Dialog"))
        val after = tap().getLong("solidAtTouch")
        assertTrue(after > first)
        assertEquals(events.solidSeq, after)
    }

    // Issue #280: a tap's reply says whether it was the first input the window under it ever
    // got, and the window's age: true only for the first touch into a window id.
    @Test
    fun firstInWindowOnlyForTheFirstTouchIntoAWindow() {
        clock.advance(10_000)
        showWindows() // the agent's first list: none (what comes after is new)
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

    // A modal (a dialog's window) comes over the app's window: the first touch into it is first.
    // When it closes, the app's window is back, with the same id: not new, and it took input
    // before, so the next touch into it is not first. (A rule on "the window changed since the
    // last touch" would take it for new and send a late-acting press twice.)
    @Test
    fun theAppsWindowBackAfterAModalClosedIsNotNew() {
        clock.advance(10_000)
        showWindows() // the agent's first list: none (what comes after is new)
        showWindows(appWindow)
        clock.advance(200)
        assertTrue(tap().getBoolean("firstInWindow"))
        val dialog = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 1080, 1400))
        showWindows(dialog, appWindow)
        clock.advance(150)
        val onDialog = tap()
        assertTrue(onDialog.getBoolean("firstInWindow"))
        assertEquals(150L, onDialog.getLong("windowAgeMs"))
        showWindows(appWindow) // the dialog closed
        clock.advance(100)
        assertFalse(tap().getBoolean("firstInWindow"))
        // The same dialog opened again is a new window (a new id), and takes its first input again.
        val reopened = FakeWindow(6, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(0, 0, 1080, 1400))
        showWindows(reopened, appWindow)
        clock.advance(100)
        assertTrue(tap().getBoolean("firstInWindow"))
        // A full-screen dialog drops the app's window from the list while it is up (seen on an
        // emulator): listed again when the dialog closes, with its id, it is still not new, and
        // its age runs from when it first came.
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

    // Issue #280 review: which class opened a window (its window-state event).
    private fun opened(w: FakeWindow, cls: String, pkg: String = "com.app", activity: Boolean = false) {
        if (activity) device.activities += "$pkg/$cls"
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, pkg, cls, windowId = w.id))
    }

    // Issue #280 re-review: an Activity's window whose first window-state event the agent saw
    // names a view's class (a drawer opening), not the Activity's (its creation event missed,
    // or late): it must not be taken for a dialog over the app. Fails closed: a framework view
    // class, or a class from an event long after the window came, says nothing.
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
            opened(next, cls) // the Activity's own event was not seen
            clock.advance(100)
            val r = tap()
            assertTrue(r.getBoolean("firstInWindow"))
            assertFalse("$cls: windowOver", r.getBoolean("windowOver"))
        }
    }

    // Review of issue #280: only a window that came in over the app may swallow a touch while it
    // comes in: one an Activity did not open (a Dialog, a sheet, a popup) that came while a
    // window of its app was listed. A new screen (an Activity) is not: a press there that acts
    // late and draws nothing must never be sent twice.
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
        // A modal dialog over it: Android takes the Activity's window off the list meanwhile.
        val dialog = FakeWindow(5, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(40, 40, 1040, 1400))
        showWindows(dialog)
        opened(dialog, "androidx.compose.ui.window.DialogWrapper")
        clock.advance(100)
        assertTrue(tap().getBoolean("windowOver"))
        // A dialog-styled Activity of another app (the permission controller's) cannot be told
        // from another app launched: neither is over the app.
        showWindows(appWindow)
        val other = FakeWindow(6, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode().apply { packageName = "com.android.permissioncontroller" })
        showWindows(other)
        opened(other, "com.android.permissioncontroller.GrantPermissionsActivity", "com.android.permissioncontroller", activity = true)
        clock.advance(100)
        val permission = tap()
        assertTrue(permission.getBoolean("firstInWindow"))
        assertFalse(permission.getBoolean("windowOver"))
        // A window whose opening was not seen: not over.
        val unseen = FakeWindow(8, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(), Box(40, 40, 1040, 1400))
        showWindows(unseen, appWindow)
        clock.advance(100)
        assertFalse(tap().getBoolean("windowOver"))
    }

    // Split screen: two Activities side by side; a new Activity replaces the right one, smaller
    // than the display: a new screen, not over the app.
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

    // Landscape with 3-button navigation: an Activity narrower than the display is a new screen.
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

    // A bottom sheet's window covers the display, over its Activity: over the app.
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

    // A swipe is input too: a scroll, then a tap in the same window, is not its first.
    @Test
    fun aSwipeIsTheWindowsInput() {
        clock.advance(10_000)
        showWindows()
        showWindows(appWindow)
        clock.advance(100)
        call("""{"id":1,"op":"swipe","x1":100,"y1":100,"x2":100,"y2":50,"settle":false}""")
        assertFalse(tap().getBoolean("firstInWindow"))
    }

    // After an agent restart, a window that was there but hidden (under a full-screen dialog)
    // is listed only later: its id is below the highest listed at the start, so it is old.
    @Test
    fun aWindowHiddenWhenTheAgentStartedIsOld() {
        clock.advance(10_000)
        probe.windows = intArrayOf(1, 5) // the app's window 3 is under the dialog 5
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

    // A key into a window is input too: a tap into it after is not its first.
    @Test
    fun aKeyIsTheWindowsFirstInput() {
        clock.advance(10_000)
        showWindows() // the agent's first list: none (what comes after is new)
        showWindows(appWindow)
        probe.activeWindow = appWindow.id
        clock.advance(100)
        call("""{"id":3,"op":"key","code":66,"settle":false}""")
        assertFalse(tap().getBoolean("firstInWindow"))
    }

    // Windows already there when the agent started, and windows older than the horizon, say
    // nothing: they are not ones coming in, and nothing is read for them.
    @Test
    fun oldWindowsSayNothingOfFirstInput() {
        clock.advance(10_000)
        probe.windows = intArrayOf(appWindow.id) // there when the agent starts
        nodes.windowList = listOf(appWindow)
        val started = Ops(device, tree, input, Events(probe, clock), clock)
        clock.advance(100)
        var reads = nodes.windowReads
        val before = JSONObject(started.handle("""{"id":9,"op":"tap","x":100,"y":100,"settle":false}""")!!.text)
        assertFalse(before.has("firstInWindow"))
        assertEquals(reads, nodes.windowReads)
        showWindows()
        showWindows(appWindow) // added now
        clock.advance(5_000) // and tapped only once it is old (Ops.FIRST_INPUT_HORIZON_MS)
        reads = nodes.windowReads
        val r = tap()
        assertFalse(r.has("firstInWindow"))
        assertFalse(r.has("windowAgeMs"))
        assertEquals(reads, nodes.windowReads)
    }

    // A tap into a window that has been there a while is not held, and nothing is read for it
    // (CI mode, where the hold is).
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

    // Only the target window's age counts: the keyboard, the status bar or a dialog elsewhere
    // coming, a window going, or the app's window changing its title or bounds never delay a tap.
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
            { showWindows(appWindow) }, // the others go
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

    // A touch the platform refuses is reported: a refused DOWN sends nothing more (no app saw a
    // tap: the host sends it again), and an UP not delivered says so after a CANCEL, so that no
    // touch is left down (the DOWN may have reached the app: the host fails the step).
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

    // Issue #175: the tap focused a field, and its keyboard is still on its way up when
    // hideKeyboard comes (on a slow emulator it shows a second after the tap). It is closed
    // once it shows, not left to come up after the command returned.
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
        assertEquals(1780L, clock.now) // up at 1600, steady at 1700, gone 80 ms after Back
    }

    // Issue #175: the tap opened a screen that focuses its field and asks for the keyboard
    // itself; the screen is not even there yet (no focused field) when hideKeyboard comes. It
    // opens slowly (changes at 700 and 1400 ms), and the keyboard shows at 1900 ms, after
    // pendingMs from the tap but within pendingMs of the screen's last change.
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

    // Issue #175: a screen that never stops changing does not hold the wait past twice
    // pendingMs.
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

    // Issue #175: with no keyboard coming, the wait ends pendingMs after the last action, with
    // no Back (which would navigate), and long after an action it does not wait at all.
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

    // Issue #175 (review): tap, type, hide with the keyboard never seen since the tap: the
    // keyboard may still be coming, so it waits pendingMs after the typing.
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

    // Issue #175, as on CI run 43213081336 (keyboard.yaml): the tap opens the autofocus screen,
    // "auto" is typed before its keyboard is up, and the keyboard shows while hideKeyboard runs.
    // Without the wait the agent replied "hidden" at once and the host then found the keyboard
    // up ("Couldn't hide the keyboard"); now it is closed once it shows.
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

    // Issue #175 (review): the keyboard was up after the tap and Enter closed it: it is not
    // still on its way, so hideKeyboard returns at once.
    @Test
    fun hidingKeyboardAfterEnterDoesNotWait() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        imeUp()
        call("""{"id":2,"op":"info"}""") // a read finds it up
        injector.onKey = { key -> if (key.action == android.view.KeyEvent.ACTION_UP) imeDown() }
        call("""{"id":3,"op":"key","code":66,"settle":false}""")
        val t = clock.now
        val r = call("""{"id":4,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertEquals(t, clock.now)
        assertEquals(listOf(66, 66), injector.keys.map { it.event.code })
    }

    // Issue #175 (review): a keyboard closed by Back gets no extra wait, and neither does a
    // second hideKeyboard right after.
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

    // Issue #175 (review): a request without pendingMs (an older host) never waits.
    @Test
    fun hidingKeyboardWithoutPendingMsDoesNotWait() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        call("""{"id":2,"op":"hideKeyboard","timeout":500}""")
        assertEquals(1000L, clock.now)
    }

    // Issue #175 (review): a keyboard that flickers up and away during the wait gets no Back,
    // which would navigate once it is gone.
    @Test
    fun hidingKeyboardSendsNoBackForAFlicker() {
        nodes.field = FakeField()
        call("""{"id":1,"op":"tap","x":10,"y":10,"settle":false}""")
        clock.postDelayed({ imeUp() }, 300)
        clock.postDelayed({ imeDown() }, 340)
        val r = call("""{"id":2,"op":"hideKeyboard","timeout":500,"pendingMs":1000}""")
        assertTrue(r.getBoolean("hidden"))
        assertTrue(injector.keys.isEmpty())
        assertEquals(2340L, clock.now) // pendingMs after the screen's last change, at 1340
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

    /**
     * A tap says whether a view reported a click: one that reached no view (its target moved
     * away under it, bench/flows/scenarios/animations.yaml's layout shift) reports none, so the
     * host can tap it where it is now.
     */
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

    /**
     * A click can be reported after the tap's reply (bench/flows/scenarios/errors.yaml: the
     * reply went out 2 ms after the tap): the reply carries the count before the tap, and
     * "clicks" the count now, for the host to compare before it taps again.
     */
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
    fun aRequestSentAgainIsAnsweredFromItsFirstRun() {
        appReactsToTaps()
        val first = ops.handle("""{"id":1,"op":"tap","x":10,"y":20,"rid":"r-1"}""")!!.text
        val again = ops.handle("""{"id":2,"op":"tap","x":10,"y":20,"rid":"r-1"}""")!!.text
        assertEquals(2, injector.touches.size) // tapped once
        assertEquals(Ops.withId(first, 2), again)
        assertEquals(2, JSONObject(again).getInt("id"))
        // Another token is another request.
        ops.handle("""{"id":3,"op":"tap","x":10,"y":20,"rid":"r-2"}""")
        assertEquals(4, injector.touches.size)
    }

    @Test
    fun onlyTheLatestRequestTokensAreKept() {
        for (i in 0..Ops.RECENT_REQUESTS) call("""{"id":$i,"op":"tap","x":1,"y":1,"settle":false,"rid":"r$i"}""")
        val taps = injector.touches.size
        call("""{"id":100,"op":"tap","x":1,"y":1,"settle":false,"rid":"r${Ops.RECENT_REQUESTS}"}""")
        assertEquals(taps, injector.touches.size)
        call("""{"id":101,"op":"tap","x":1,"y":1,"settle":false,"rid":"r0"}""") // forgotten: runs again
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
