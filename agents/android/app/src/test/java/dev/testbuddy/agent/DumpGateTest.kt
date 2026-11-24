package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class DumpGateTest {
    private val held = ArrayList<Runnable>()
    private var reads = 0
    private var closes = 0

    private fun gate(runNow: Boolean) = DumpGate { if (runNow) it.run() else held += it }

    private fun DumpGate.ask(service: String = "input_method", fail: Throwable? = null, out: String = "x", timedOut: Boolean = false) =
        run(service, dump = { if (fail != null) throw fail }, closeWrite = { closes++ },
            read = { reads++; out.toByteArray() to timedOut })

    @Test
    fun aDumpIsItsOutput() {
        val r = gate(runNow = true).ask(out = "editor")!!
        assertEquals("editor", r.out)
        assertEquals(0, r.code)
        assertEquals(1, closes)
    }

    @Test
    fun whileADumpIsStuckNoOtherStartsAndTheAnswerIsTimedOutAtOnce() {
        val g = gate(runNow = false)
        assertTrue(g.ask(out = "", timedOut = true)!!.timedOut)
        assertEquals(1, held.size)
        repeat(3) {
            val r = g.ask()!!
            assertTrue("a stuck service: timed out at once", r.timedOut)
            assertEquals(-1, r.code)
        }
        assertEquals("no new dump thread while one is stuck", 1, held.size)
        assertEquals("nothing read while one is stuck", 1, reads)
        assertEquals("each refused dump's write end closed", 3, closes)
        assertEquals("y", g.ask(service = "window", out = "y")!!.out)
        held.removeAt(1).run()
        held.removeAt(0).run()
        assertEquals(0, g.ask(out = "z", timedOut = false).let { held.removeAt(0).run(); it!!.code })
    }

    @Test
    fun aDumpCallThatThrowsIsNullSoDumpsysRuns() {
        for (t in listOf(SecurityException("no DUMP"), IllegalStateException("dead object"), IOException("pipe"))) {
            val g = gate(runNow = true)
            assertNull("${t.javaClass.simpleName}: not an empty dump", g.ask(fail = t, out = ""))
            assertEquals("the gate is free again", "ok", g.ask(out = "ok")!!.out)
        }
    }

    @Test
    fun aReadThatFailsIsNull() {
        assertNull(gate(runNow = true).run("input_method", {}, {}, { throw IOException("read") }))
    }
}
