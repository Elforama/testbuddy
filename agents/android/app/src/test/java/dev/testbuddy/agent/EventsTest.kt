package dev.testbuddy.agent

import android.view.accessibility.AccessibilityEvent
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** How the agent decides the UI changed and settled, on fake events and a fake clock. */
class EventsTest {
    private val clock = FakeClock(1_000)
    private val probe = FakeProbe()
    private val pushed = ArrayList<String>()
    private val events by lazy { Events(probe, clock).also { e -> e.sink = { pushed += it } } }

    private fun content(pkg: String = "com.app", source: Long? = null, textOnly: Boolean = false) = UiEvent(
        AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, pkg, "android.widget.TextView",
        if (textOnly) AccessibilityEvent.CONTENT_CHANGE_TYPE_TEXT else AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE, source,
    )

    private fun window(pkg: String) = UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, pkg, "android.app.Activity")

    @After fun noToast() { Toasts.texts = emptyList(); Toasts.until = 0 }

    @Test
    fun aChangeSettlesOnceQuiet() {
        val since = events.solid
        clock.postDelayed({ events.onEvent(content()) }, 40) // the app reacts to the action
        assertEquals(Events.Settle.CHANGED, events.awaitSettled(since, 250, 5000))
        // Settled one quiet window after the change, no later.
        assertEquals(1_000L + 40 + Events.DEFAULT_QUIET_MS, clock.now)
        assertEquals(listOf("{\"ev\":\"changed\",\"seq\":1,\"pkg\":\"com.app\"}", "{\"ev\":\"settled\",\"seq\":1}"), pushed)
    }

    @Test
    fun nothingChangesWithinNoChange() {
        assertEquals(Events.Settle.NO_CHANGE, events.awaitSettled(events.solid, 250, 5000))
        assertEquals(1_250L, clock.now)
        assertTrue(pushed.isEmpty())
    }

    @Test
    fun aTimeoutShorterThanNoChangeEndsAtTheTimeout() {
        // No change within the timeout is no change: it must not wait on past the deadline.
        assertEquals(Events.Settle.NO_CHANGE, events.awaitSettled(events.solid, 250, 100))
        assertEquals(1_100L, clock.now)
    }

    @Test
    fun aZeroTimeoutReturnsAtOnce() {
        assertEquals(Events.Settle.NO_CHANGE, events.awaitSettled(events.solid, 250, 0))
        assertEquals(1_000L, clock.now)
    }

    @Test
    fun aScreenThatNeverStopsChangingTimesOut() {
        val since = events.solid
        // Changes with no known source can never be told apart from the app working.
        clock.repeat(clock.now + 10, 50, clock.now + 10_000) { events.onEvent(content()) }
        assertEquals(Events.Settle.TIMEOUT, events.awaitSettled(since, 250, 5000))
        assertEquals(6_000L, clock.now)
    }

    @Test
    fun aTickerStopsHoldingTheScreenUnsettled() {
        val since = events.solid
        // A stopwatch: the same view's text, every 50 ms, for good.
        clock.repeat(clock.now + 10, 50, clock.now + 10_000) { events.onEvent(content(source = 42L, textOnly = true)) }
        assertEquals(Events.Settle.CHANGED, events.awaitSettled(since, 250, 5000))
        // Restless after RESTLESS_AFTER_MS of changes, then quiet: well before the timeout.
        assertTrue("settled at ${clock.now}", clock.now < 1_000 + Events.RESTLESS_AFTER_MS + 300)
        // Its changes still count, so the host re-reads the screen on them.
        val seq = events.seq
        clock.advance(200)
        assertTrue(events.seq > seq)
        assertEquals(Events.Settle.NO_CHANGE, events.awaitSettled(events.solid, 250, 5000))
    }

    @Test
    fun reactOnlyReturnsOnceTheChangeStarts() {
        val since = events.solid
        clock.postDelayed({ events.onEvent(content()) }, 30)
        assertEquals(Events.Settle.CHANGED, events.awaitSettled(since, 250, 5000, reactOnly = true))
        assertEquals(1_030L, clock.now)
    }

    @Test
    fun theStatusBarIsNoChange() {
        events.onEvent(content(pkg = "com.android.systemui"))
        assertEquals(0L, events.seq)
        // Unless the system UI is what is in front (the notification shade).
        events.onEvent(window("com.android.systemui"))
        events.onEvent(content(pkg = "com.android.systemui"))
        assertEquals(2L, events.seq)
    }

    @Test
    fun theKeyboardRedrawingIsNoChangeInTheApp() {
        probe.settings["secure/default_input_method"] = "com.ime/.Service"
        events.onEvent(content(pkg = "com.ime"))
        assertEquals(1L, events.seq)
        assertEquals(0L, events.solid)
    }

    @Test
    fun irrelevantEventsAreIgnored() {
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_VIEW_HOVER_ENTER, "com.app", null))
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_ANNOUNCEMENT, "com.app", null))
        assertEquals(0L, events.seq)
    }

    @Test
    fun framesDrawnAfterAnActionHoldItUnsettled() {
        probe.activeWindow = 7
        // The window keeps drawing (a sheet sliding in) with no accessibility events.
        probe.framePresented = { clock.nanoTime() - 10_000_000 }
        val since = events.solid
        events.beginAction()
        assertEquals(listOf(7), probe.cleared)
        events.onEvent(content())
        assertEquals(Events.Settle.CHANGED, events.awaitSettled(since, 250, 5000))
        // Held for its frames, up to FRAME_GRACE_MS past the last event.
        assertTrue("settled at ${clock.now}", clock.now >= 1_000 + Events.FRAME_GRACE_MS)
        assertTrue("settled at ${clock.now}", clock.now < 1_000 + Events.FRAME_GRACE_MS + Events.DEFAULT_QUIET_MS + 10)
    }

    @Test
    fun withAnimationsOffARestlessViewsFramesDoNotHoldIt() {
        probe.settings["global/animator_duration_scale"] = "0"
        assertTrue(events.animationsOff)
        probe.activeWindow = 7
        probe.framePresented = { clock.nanoTime() - 5_000_000 }
        val since = events.solid
        events.beginAction()
        clock.repeat(clock.now + 10, 50, clock.now + 10_000) { events.onEvent(content(source = 9L, textOnly = true)) }
        assertEquals(Events.Settle.CHANGED, events.awaitSettled(since, 250, 5000))
        assertTrue("settled at ${clock.now}", clock.now < 1_000 + Events.RESTLESS_AFTER_MS + 300)
    }

    @Test
    fun quietWaitsForThePendingChange() {
        events.onEvent(content())
        assertTrue(events.awaitQuiet(1000))
        assertEquals(1_000L + Events.DEFAULT_QUIET_MS, clock.now)
        clock.repeat(clock.now + 10, 50, clock.now + 10_000) { events.onEvent(content()) }
        clock.advance(20)
        assertFalse(events.awaitQuiet(500))
    }

    @Test
    fun aLongerQuietWindowSettlesLater() {
        events.quietMs = 400
        events.onEvent(content())
        assertTrue(events.awaitQuiet(1000))
        assertEquals(1_400L, clock.now)
    }

    @Test
    fun aToastIsAChangeWhenItShowsAndWhenItGoes() {
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED, "com.app", "android.widget.Toast\$TN", texts = listOf("Saved", " ")))
        assertEquals(listOf("Saved"), Toasts.showing(clock.now))
        assertEquals(1L, events.solid)
        clock.advance(Toasts.SHOWN_MS + 1)
        assertTrue(Toasts.showing(clock.now).isEmpty())
        assertEquals(2L, events.solid)
    }

    @Test
    fun aNotificationThatIsNoToastIsIgnored() {
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED, "com.app", "android.app.Notification", texts = listOf("Mail")))
        assertEquals(0L, events.seq)
        assertTrue(Toasts.showing(clock.now).isEmpty())
    }

    @Test
    fun awaitPackageWakesOnTheWindowChange() {
        probe.activePkg = "com.launcher"
        clock.postDelayed({
            probe.activePkg = "com.app"
            events.onEvent(window("com.app"))
        }, 300)
        assertTrue(events.awaitPackage("com.app", 10_000))
        assertEquals(1_300L, clock.now)
        assertEquals("com.app", events.foregroundPackage)
    }

    @Test
    fun awaitPackageGivesUp() {
        probe.activePkg = "com.launcher"
        assertFalse(events.awaitPackage("com.app", 2_000))
        assertEquals(3_000L, clock.now)
    }

    /** Issue #211: the window an app's latest window-state event came from is the screen it
     *  opened, which a tree read looks for; the keyboard's and the status bar's are not. */
    @Test
    fun theWindowAnAppOpenedIsKept() {
        probe.settings["secure/default_input_method"] = "com.ime/.Service"
        val state = { pkg: String, w: Int -> UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, pkg, "android.app.Activity", windowId = w) }
        assertEquals(null, events.openedWindow)
        clock.now = 2_000
        events.onEvent(state("com.app", 7))
        assertEquals(7, events.openedWindow?.id)
        assertEquals(2_000L, events.openedWindow?.at)
        events.onEvent(state("com.ime", 8))
        events.onEvent(state("com.android.systemui", 9))
        events.onEvent(content(pkg = "com.app"))
        events.onEvent(window("com.app")) // no window id
        assertEquals(7, events.openedWindow?.id)
        events.onEvent(state("com.app", 10))
        assertEquals(10, events.openedWindow?.id)
    }

    /** Issue #211 review: a pane's change (contentChangeTypes set) and the system's own windows do
     *  not count as a window opened: a late event from the screen left must not replace it. */
    @Test
    fun aPaneChangeOrTheSystemDoesNotReplaceTheOpenedWindow() {
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.app", "android.app.Activity", windowId = 7))
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.app", "android.widget.FrameLayout",
            AccessibilityEvent.CONTENT_CHANGE_TYPE_PANE_TITLE, windowId = 5))
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "android", "android.app.Dialog", windowId = 9))
        assertEquals(7, events.openedWindow?.id)
    }

    /** Issue #211 review: a keyboard switched to after the agent started is learned from its
     *  first window-state event, which then does not stand as the window opened. */
    @Test
    fun aKeyboardSwitchedToIsNotTheOpenedWindow() {
        probe.settings["secure/default_input_method"] = "com.ime/.Service"
        val ev = Events(probe, clock) { it.run() }
        ev.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.app", "android.app.Activity", windowId = 7))
        probe.settings["secure/default_input_method"] = "com.other.ime/.Service"
        val kb = UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.other.ime", "android.inputmethodservice.SoftInputWindow", windowId = 8)
        ev.onEvent(kb)
        assertEquals(null, ev.openedWindow)
        ev.onEvent(kb)
        assertEquals(null, ev.openedWindow)
    }
}
