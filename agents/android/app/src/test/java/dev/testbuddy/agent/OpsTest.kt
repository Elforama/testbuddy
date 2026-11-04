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

    /** The app redraws 30 ms after a touch lifts. */
    private fun appReactsToTaps() {
        injector.onTouch = { t ->
            if (t.action == MotionEvent.ACTION_UP) clock.postDelayed({
                events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null))
            }, 30)
        }
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
