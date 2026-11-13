package dev.testbuddy.agent

import android.view.accessibility.AccessibilityEvent
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * How pushed events reach the host: on a writer thread of the connection's own, in order, after
 * the hello (issue #71/#87 review).
 */
class OutboxTest {
    private val clock = FakeClock(1_000)
    private val events by lazy { Events(FakeProbe(), clock) }
    private val written = Collections.synchronizedList(ArrayList<String>())
    private val outboxes = ArrayList<Outbox>()

    private fun content() = UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, "com.app", "android.widget.TextView",
        AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE)

    private fun outbox(write: (String) -> Unit) = Outbox(write).also { outboxes += it }

    @After fun close() { outboxes.forEach { it.close() } }

    private fun awaitWritten(n: Int) {
        val until = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (written.size < n && System.nanoTime() < until) Thread.sleep(5)
        assertTrue("written: $written", written.size >= n)
    }

    /**
     * A host that reads slowly (a big reply on a slow adb forward) blocks the socket write. The
     * event and timer threads only queue: the quiet timer still settles on time, and the events
     * follow in order once the writer is free.
     */
    @Test
    fun aBlockedWriterDoesNotDelaySettling() {
        val release = CountDownLatch(1)
        val box = outbox { msg -> release.await(5, TimeUnit.SECONDS); written += msg }
        events.sink = box::offer
        box.start()
        events.onEvent(content())
        assertTrue(events.awaitQuiet(1000))
        assertEquals(1_000L + Events.DEFAULT_QUIET_MS, clock.now) // settled on time
        assertTrue("written while blocked: $written", written.isEmpty())
        release.countDown()
        awaitWritten(2)
        assertEquals(listOf("{\"ev\":\"changed\",\"seq\":1,\"pkg\":\"com.app\"}", "{\"ev\":\"settled\",\"seq\":1}"), written)
    }

    /**
     * Pushes used to be written as soon as the sink was set, before the hello: the host's Dial
     * then took the first line for no hello and dropped the connection. A change while the hello
     * is made is queued, and written after it.
     */
    @Test
    fun theHelloGoesFirst() {
        val box = outbox { written += it }
        greet(events, box, hello = {
            events.onEvent(content()) // a change while the hello is made
            "{\"ev\":\"hello\",\"seq\":${events.seq}}"
        }, send = { written += it })
        events.onEvent(content())
        clock.advance(Events.COALESCE_MS) // its trailing push
        awaitWritten(3)
        assertEquals("{\"ev\":\"hello\",\"seq\":1}", written[0])
        assertTrue("written: $written", written.drop(1).all { it.startsWith("{\"ev\":\"changed\"") })
    }

    /** Nothing queued is written before start, and nothing after close. */
    @Test
    fun startAndClose() {
        val box = outbox { written += it }
        box.offer("a")
        Thread.sleep(50)
        assertTrue(written.isEmpty())
        box.start()
        awaitWritten(1)
        box.close()
        box.offer("b")
        Thread.sleep(50)
        assertEquals(listOf("a"), written)
    }
}
