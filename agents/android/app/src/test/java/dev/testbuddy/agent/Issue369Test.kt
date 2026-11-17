package dev.testbuddy.agent

import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityWindowInfo
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Issue #369: before a tap the host read its target again, 40 ms apart, until two reads agreed,
 * even on a screen where nothing moved. A read past the cache now says, per window, how long the
 * window has drawn no frame ("still", ms), when its frames show motion: the host takes the
 * target at rest on that read when it is long enough.
 */
class Issue369Test {
    private val clock = FakeClock(1_000)
    private val probe = FakeProbe()
    private val events = Events(probe, clock)
    private val nodes = FakeNodes()
    private val tree = Tree(nodes) { clock.now }
    private val input = Input(FakeInjector(), clock) { nodes.field }
    private val ops = Ops(FakeDevice(), tree, input, events, clock)

    private fun call(req: String): JSONObject = JSONObject(ops.handle(req)!!.text)

    @After fun noToast() { Toasts.texts = emptyList(); Toasts.until = 0 }

    init {
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode(children = listOf(FakeNode("android.widget.Button", "Go")))))
    }

    private fun change() = events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app",
        "android.widget.TextView", AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE, 77L))

    /** The window presented its latest frame at uptime [at] ms. */
    private fun drewAt(at: Long) { probe.framePresented = { at * 1_000_000 } }

    private fun window(r: JSONObject) = r.getJSONArray("windows").getJSONObject(0)

    @Test
    fun aFreshReadSaysHowLongTheWindowHasDrawnNothing() {
        change()
        drewAt(clock.now + 16) // the change's frame
        clock.advance(300)
        val w = window(call("""{"id":1,"op":"tree","fresh":true}"""))
        assertTrue("a read past the cache says how long the window has been still", w.has("still"))
        assertEquals(284L, w.getLong("still"))
    }

    @Test
    fun aWindowStillDrawingIsNotStill() {
        change()
        clock.advance(300)
        drewAt(clock.now - 8) // a frame 8 ms ago: something moves, with no event
        assertEquals(8L, window(call("""{"id":1,"op":"tree","fresh":true}""")).getLong("still"))
    }

    @Test
    fun aPlainReadSaysNothing() {
        change()
        drewAt(clock.now)
        clock.advance(300)
        assertFalse(window(call("""{"id":1,"op":"tree"}""")).has("still"))
    }

    @Test
    fun aWindowThatDrewNothingForTheLatestChangeIsNotTrusted() {
        // Its content draws elsewhere (a Flutter app's SurfaceView): the change came with no
        // frame of the window's own, so its frames say nothing of what moves.
        drewAt(clock.now)
        clock.advance(1_000)
        change()
        clock.advance(300)
        assertFalse(window(call("""{"id":1,"op":"tree","fresh":true}""")).has("still"))
    }

    @Test
    fun noFrameStatsNoStill() {
        change()
        clock.advance(300)
        assertFalse(window(call("""{"id":1,"op":"tree","fresh":true}""")).has("still"))
    }
}
