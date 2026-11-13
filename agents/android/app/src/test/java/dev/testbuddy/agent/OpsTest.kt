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
        val r = call("""{"id":1,"op":"type","text":"abc","verifyFirst":true,"settle":false}""")
        assertTrue(r.getBoolean("ok"))
        assertEquals("abc", field.value)
        assertEquals(2000L, clock.now)
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
    // window only once it is 80 ms old. Without the wait (age 0) the tap 50 ms after the window
    // came is dropped: the platform reports its UP not delivered. With it, the tap is held until
    // the window is 100 ms old (400 ms in CI mode), and lands.
    @Test
    fun tapIntoAWindowJustAddedWaitsUntilItTakesInput() {
        for ((age, ci) in listOf(0L to false, Ops.INPUT_WINDOW_AGE_MS to false, Ops.CI_INPUT_WINDOW_AGE_MS to true)) {
            injector.touches.clear()
            clock.advance(10_000)
            showWindows() // the window goes, then comes again: new
            val added = clock.now
            showWindows(appWindow)
            // An async DOWN is queued either way; the dispatcher's verdict comes with the sync UP.
            injector.accept = { _, sync -> !sync || clock.now - added >= 80 }
            clock.advance(50)
            ops.inputWindowAgeMs = if (ci) Ops.INPUT_WINDOW_AGE_MS else age
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
    // heard of it: an event that reached the agent 60 ms late leaves 40 ms to wait.
    @Test
    fun windowAgeRunsFromTheEventsTime() {
        clock.advance(10_000)
        showWindows(appWindow, addedAt = clock.now - 60)
        assertEquals(40L, tap().getLong("windowWaitMs"))
    }

    // A tap into a window that has been there a while is not held, and nothing is read for it.
    @Test
    fun tapIntoAnOldWindowDoesNotWait() {
        showWindows(appWindow)
        clock.advance(5_000)
        val reads = nodes.windowReads
        val at = clock.now
        val r = tap()
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
            val r = tap()
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
