package dev.testbuddy.agent

import android.view.accessibility.AccessibilityEvent
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * Issue #74: `settle`'s `since` is a seq (every change), the `editorTarget` reply's `seq` is
 * one too, and the agent's waits read the device with the events lock free.
 */
class Issue74Test {
    private val clock = FakeClock(1_000)
    private val probe = FakeProbe()
    private val events = Events(probe, clock)
    private val nodes = FakeNodes()
    private val tree = Tree(nodes) { clock.now }
    private val input = Input(FakeInjector(), clock) { nodes.field }
    private val ops = Ops(FakeDevice(), tree, input, events, clock)

    private fun call(req: String): JSONObject = JSONObject(ops.handle(req)!!.text)

    @After fun noToast() { Toasts.texts = emptyList(); Toasts.until = 0 }

    private fun tick() = events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app",
        "android.widget.TextView", AccessibilityEvent.CONTENT_CHANGE_TYPE_TEXT, 9L))

    private fun solidChange() = events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app",
        "android.widget.EditText", AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE, 77L))

    /** A ticker: one view whose text changes every 100 ms, learned as restless, still going. */
    private fun startTicker() {
        repeat(8) { tick(); clock.advance(100) }
        clock.advance(Events.DEFAULT_QUIET_MS.toLong() + 10) // its first changes settle
        assertTrue("the ticker moved seq past solid", events.seq > events.solid)
        clock.repeat(clock.now + 100, 100, clock.now + 60_000) { tick() }
    }

    /** Issue #74: docs/protocol.md's `since` is a seq; on a screen with a ticker, seq is ahead
     *  of the solid count, and a host passing the seq it saw got "changed" at once. */
    @Test
    fun settleSinceTheSeqIsNoChangeWhenOnlyATickerMoves() {
        startTicker()
        val r = call("""{"id":1,"op":"settle","since":${events.seq},"noChange":250,"timeout":2000}""")
        assertEquals("no_change", r.getString("result"))
    }

    @Test
    fun settleSinceTheSeqSeesASolidChange() {
        startTicker()
        val since = events.seq
        clock.postDelayed({ solidChange() }, 50)
        val r = call("""{"id":1,"op":"settle","since":$since,"noChange":250,"timeout":2000}""")
        assertEquals("changed", r.getString("result"))
    }

    /** Issue #74: `editorTarget` put the solid count under the name `seq`. */
    @Test
    fun editorTargetsSeqIsTheSeq() {
        nodes.field = object : Field by FakeField(value = "hello") {
            override fun editorTarget(): EditorTarget = EditorTarget("com.app", 7, 42, "field", "EditText")
        }
        startTicker()
        val prepared = call("""{"id":1,"op":"editorTarget"}""")
        assertEquals(events.seq, prepared.getLong("seq"))
        // The host settles on it after the editor's Backspace: a ticker alone is no change ...
        val quiet = call("""{"id":2,"op":"settle","since":${prepared.getLong("seq")},"noChange":250,"timeout":2000}""")
        assertEquals("no_change", quiet.getString("result"))
        // ... the field's text changing is one.
        val prepared2 = call("""{"id":3,"op":"editorTarget"}""")
        clock.postDelayed({ solidChange() }, 30)
        val changed = call("""{"id":4,"op":"settle","since":${prepared2.getLong("seq")},"noChange":250,"timeout":2000}""")
        assertEquals("changed", changed.getString("result"))
    }

    /** Issue #74: awaitCondition ran its condition (accessibility reads) holding the events
     *  lock, so events could not be taken in while it read. */
    @Test(timeout = 10_000)
    fun awaitConditionReadsWithTheLockFree() {
        val reading = CountDownLatch(1)
        val eventTaken = CountDownLatch(1)
        val waiter = Thread {
            events.awaitCondition(5_000) {
                reading.countDown()
                eventTaken.await(2, TimeUnit.SECONDS) // a slow accessibility read
            }
        }.apply { start() }
        assertTrue(reading.await(2, TimeUnit.SECONDS))
        val event = Thread { solidChange(); eventTaken.countDown() }.apply { start() }
        event.join(1_000)
        assertFalse("an event waited for the condition's read", event.isAlive)
        waiter.join(5_000)
    }

    /** Issue #74: the same for awaitPackage's foreground read. */
    @Test(timeout = 10_000)
    fun awaitPackageReadsWithTheLockFree() {
        val reading = CountDownLatch(1)
        val eventTaken = CountDownLatch(1)
        val slow = object : UiProbe by probe {
            override fun activePackage(): String? {
                reading.countDown()
                eventTaken.await(2, TimeUnit.SECONDS)
                return "com.app"
            }
        }
        val e = Events(slow, clock)
        val waiter = Thread { e.awaitPackage("com.app", 5_000) }.apply { start() }
        assertTrue(reading.await(2, TimeUnit.SECONDS))
        val event = Thread {
            e.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", null, AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE))
            eventTaken.countDown()
        }.apply { start() }
        event.join(1_000)
        assertFalse("an event waited for awaitPackage's read", event.isAlive)
        waiter.join(5_000)
    }

    /** A wait still wakes on every event: a condition that turns true on one is seen. */
    @Test
    fun awaitConditionStillWakesOnEvents() {
        var shown = true
        clock.postDelayed({ shown = false; solidChange() }, 300)
        assertTrue(events.awaitCondition(5_000) { !shown })
        assertEquals(1_300L, clock.now)
    }

    /** Issue #74: with the reads outside the lock, an event during every read must not keep a
     *  wait going past its deadline (a screen that never stops changing). */
    @Test(timeout = 10_000)
    fun waitsEndAtTheirDeadlineWhileEventsKeepComing() {
        assertFalse(events.awaitCondition(1_000) { solidChange(); clock.advance(100); false })
        assertTrue("ended at ${clock.now}", clock.now in 2_000L..2_100L)
        lateinit var e: Events
        e = Events(object : UiProbe by probe {
            override fun activePackage(): String? {
                // A window comes or goes during every read.
                e.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOWS_CHANGED, "com.other", null))
                clock.advance(100)
                return "com.other"
            }
        }, clock)
        val start = clock.now
        assertFalse(e.awaitPackage("com.app", 1_000))
        assertTrue("ended ${clock.now - start} ms after the start", clock.now - start in 1_000L..1_100L)
    }

    /** Issue #74: a timed-out shell command killed only `sh`: a command it started kept
     *  running, holding the output pipe and the agent's reader thread. */
    @Test(timeout = 30_000)
    fun aTimedOutShellKillsWhatItStarted() {
        val dir = File(System.getProperty("java.io.tmpdir"), "tb-shell-${System.nanoTime()}").apply { mkdirs() }
        val pidFile = File(dir, "pid")
        try {
            val started = System.nanoTime()
            val res = Shell.run("sleep 30 & echo \$! > ${pidFile.path}; wait", 500)
            val tookMs = (System.nanoTime() - started) / 1_000_000
            assertTrue(res.timedOut)
            val pid = pidFile.readText().trim()
            val alive = ProcessBuilder("sh", "-c", "kill -0 $pid 2>/dev/null").start().waitFor() == 0
            if (alive) ProcessBuilder("kill", "-9", pid).start().waitFor()
            assertFalse("the command's child $pid outlived the timeout", alive)
            assertTrue("returned ${tookMs} ms after the start", tookMs < 1_500)
            assertFalse("a shell reader thread is left", Thread.getAllStackTraces().keys.any { it.name == Shell.READER && it.isAlive })
        } finally {
            dir.deleteRecursively()
        }
    }

    /** Issue #74 (review): the parent pid read from /proc/<pid>/stat, whatever the command's name. */
    @Test
    fun theParentIsReadFromAStatLine() {
        assertEquals(1, Shell.parentInStat("1234 (sh) S 1 1234 1234 0 -1 4194560 120"))
        assertEquals(812, Shell.parentInStat("4321 (sleep) S 812 4321 812 0 -1 4194304 92 0"))
        // A name with spaces and parentheses: the fields start after the last ')'.
        assertEquals(77, Shell.parentInStat("99 (my (odd) name) ) R 77 99 99 0 -1"))
        assertEquals(3, Shell.parentInStat("5 (a b c) S 3 5 5\n"))
        assertEquals(null, Shell.parentInStat(""))
        assertEquals(null, Shell.parentInStat("12 (cut"))
    }

    @Test
    fun descendantsAreFoundThroughTheirParents() {
        val parents = mapOf(10 to 1, 11 to 10, 12 to 11, 13 to 10, 20 to 1)
        assertEquals(setOf(11, 12, 13), Shell.descendants(10, parents).toSet())
        assertTrue(Shell.descendants(20, parents).isEmpty())
    }
}
