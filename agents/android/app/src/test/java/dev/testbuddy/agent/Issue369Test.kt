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
 * even on a screen where nothing moved. It now asks the agent (op restProof) whether the
 * target's window is proven at rest for the span those reads compare, from the read on: no
 * frame from 50 ms before the read began to the span's end, nothing changed meanwhile, and the
 * window's frames show its motion. Anything else is not proven, and the host reads again.
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

    private val button = FakeNode("android.widget.Button", "Go")

    init {
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION, FakeNode(children = listOf(button))))
        events.animationsOff = true
    }

    private fun change() = events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app",
        "android.widget.TextView", AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE))

    /** The window presented its latest frame at uptime [at] ms (-1: one is being drawn). */
    private fun drewAt(at: Long) { probe.framePresented = { if (at < 0) -1 else at * 1_000_000 } }

    /** The app changed (three times, its window drawing each), then nothing for [quietMs]. */
    private fun changedAndDrew(quietMs: Long = 300) {
        repeat(3) { change(); clock.advance(5) }
        drewAt(clock.now)
        clock.advance(quietMs)
    }

    private fun read(fresh: Boolean = true): Long =
        call("""{"id":1,"op":"tree"${if (fresh) ",\"fresh\":true" else ""}}""").getLong("read")

    private fun proof(read: Long, span: Long = 40): JSONObject =
        call("""{"id":2,"op":"restProof","read":$read,"window":1,"span":$span}""")

    @Test
    fun aStillWindowIsProvenAtRestAfterTheSpan() {
        changedAndDrew()
        val start = clock.now
        val r = proof(read(), span = 80)
        assertTrue("a window still since well before the read is proven at rest: ${r.optString("why")}", r.optBoolean("still"))
        assertTrue("the proof waits out the span from the read's start", clock.now >= start + 80)
    }

    @Test
    fun aFrameDuringTheReadIsMotion() {
        changedAndDrew()
        val read = read()
        drewAt(clock.now + 5) // a translation starts 5 ms into the walk: the window draws
        assertFalse(proof(read).optBoolean("still"))
    }

    @Test
    fun aFrameJustBeforeTheReadIsMotion() {
        changedAndDrew()
        drewAt(clock.now - 30)
        assertFalse(proof(read()).optBoolean("still"))
    }

    @Test
    fun aFrameBeingDrawnIsMotion() {
        changedAndDrew()
        drewAt(-1)
        assertFalse(proof(read()).optBoolean("still"))
    }

    @Test
    fun aChangeAfterTheReadBeganVoidsTheProof() {
        changedAndDrew()
        val read = read()
        clock.advance(10)
        change() // an event in flight when the read began (a dialog's window coming in)
        assertFalse(proof(read).optBoolean("still"))
    }

    /** A dialog's window comes in (the app's latest changes, its window drawing), then [quietMs]. */
    private fun dialogCameIn(quietMs: Long) {
        events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED, "com.app", "android.app.Dialog"))
        clock.advance(5)
        change(); clock.advance(5)
        change()
        drewAt(clock.now)
        clock.advance(quietMs)
    }

    @Test
    fun aWindowJustAddedWithAnimationsOnIsNotProven() {
        events.animationsOff = false
        dialogCameIn(150) // its enter animation may still play, with no frame of its content
        val r = proof(read())
        assertFalse(r.optBoolean("still"))
        assertTrue(r.optString("why"), r.optString("why").contains("a window changed"))
    }

    @Test
    fun aWindowJustAddedWithAnimationsOffIsProven() {
        dialogCameIn(150)
        val r = proof(read())
        assertTrue(r.optString("why"), r.optBoolean("still"))
    }

    @Test
    fun aWindowThatDrewForOnlyTheLatestChangeIsNotTrusted() {
        // Its content draws elsewhere (a Flutter app's SurfaceView): it drew once (insets), near
        // the latest change, but not for the ones before.
        change(); clock.advance(500)
        change(); clock.advance(500)
        change()
        drewAt(clock.now)
        clock.advance(300)
        assertFalse(proof(read()).optBoolean("still"))
    }

    @Test
    fun aWindowShowingASurfaceViewIsNotTrusted() {
        nodes.windowList = listOf(FakeWindow(1, AccessibilityWindowInfo.TYPE_APPLICATION,
            FakeNode(children = listOf(button, FakeNode("android.view.SurfaceView")))))
        changedAndDrew()
        assertFalse(proof(read()).optBoolean("still"))
    }

    @Test
    fun aCachedReadIsNotProven() {
        changedAndDrew()
        assertFalse(proof(read(fresh = false)).optBoolean("still"))
    }

    @Test
    fun aReadNoLongerKeptIsNotProven() {
        changedAndDrew()
        val r = proof(12345)
        assertFalse(r.optBoolean("still"))
        assertEquals("the read is not kept", r.getString("why"))
    }
}
