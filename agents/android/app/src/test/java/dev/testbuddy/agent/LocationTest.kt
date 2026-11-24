package dev.testbuddy.agent

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LocationTest {
    private val clock = FakeClock(1_000)
    private val probe = FakeProbe()
    private val events = Events(probe, clock)
    private val nodes = FakeNodes()
    private val tree = Tree(nodes) { clock.now }
    private val input = Input(FakeInjector(), clock) { nodes.field }
    private val device = FakeDevice()
    private var inline = true
    private val queued = ArrayList<Runnable>()
    private val ops = Ops(device, tree, input, events, clock) { if (inline) it.run() else queued += it }
    private val host = Any()

    private fun call(req: String, client: Any = host): JSONObject = JSONObject(ops.handle(req, client)!!.text)

    private fun fixes(at: String) = device.shells.count { it.contains("set-test-provider-location") && it.contains("--location $at ") }

    @Test
    fun oneRequestKeepsTheFixComingEverySecond() {
        val r = call("""{"id":1,"op":"location","lat":45.5,"lon":-73.5}""")
        assertTrue(r.toString(), r.getBoolean("ok"))
        assertEquals("[\"gps\",\"network\",\"fused\"]", r.getJSONArray("providers").toString())
        for (p in listOf("gps", "network", "fused")) {
            assertTrue(device.shells.toString(), device.shells.any { it == "cmd location providers add-test-provider $p" })
            assertTrue(device.shells.any { it == "cmd location providers set-test-provider-enabled $p true" })
        }
        val listed = device.shells.indexOf("echo gps network fused > ${Location.LIST}")
        assertTrue(device.shells.toString(), listed >= 0)
        assertTrue(device.shells.toString(), listed < device.shells.indexOfFirst { it.contains("add-test-provider") })
        val set = device.shells.last()
        for (p in listOf("gps", "network", "fused")) {
            assertTrue(set, set.contains("cmd location providers set-test-provider-location $p --location 45.5,-73.5 --accuracy 1"))
        }
        assertEquals(1, fixes("45.5,-73.5"))
        clock.advance(3_500)
        assertEquals("the point sent at once, then once a second", 4, fixes("45.5,-73.5"))
    }

    @Test
    fun aNewPointReplacesTheRepeatedOne() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        clock.advance(600)
        call("""{"id":2,"op":"location","lat":3,"lon":4}""")
        assertEquals(1, device.shells.count { it == "cmd location providers add-test-provider gps" })
        clock.advance(2_500)
        assertEquals(1, fixes("1.0,2.0"))
        assertEquals("sent at once, then 1 s after the new point (not 400 ms), twice", 3, fixes("3.0,4.0"))
    }

    @Test
    fun offStopsTheRepeatsAndRemovesTheProviders() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        val off = call("""{"id":2,"op":"location","off":true}""")
        assertTrue(off.getBoolean("ok"))
        assertTrue(device.shells.last(), device.shells.last().contains("cmd location providers remove-test-provider fused"))
        assertTrue(device.shells.last(), device.shells.last().endsWith("; rm -f ${Location.LIST}"))
        val n = device.shells.size
        clock.advance(5_000)
        assertEquals(device.shells.drop(n).toString(), n, device.shells.size)
        call("""{"id":3,"op":"location","off":true}""")
        assertEquals(n, device.shells.size)
    }

    @Test
    fun aReconnectWithinTheGraceKeepsThePoint() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        ops.clientGone(Any())
        ops.clientGone(host)
        clock.advance(10_000)
        assertEquals("the repeats go on after the drop", 11, fixes("1.0,2.0"))
        assertFalse(device.shells.any { it.contains("remove-test-provider") })
        val next = Any()
        call("""{"id":1,"op":"location","lat":1,"lon":2}""", next)
        assertEquals("the providers stay: added once", 1, device.shells.count { it == "cmd location providers add-test-provider gps" })
        clock.advance(Location.GRACE_MS + 5_000)
        assertFalse(device.shells.any { it.contains("remove-test-provider") })
        assertEquals(12 + (Location.GRACE_MS + 5_000) / 1_000, fixes("1.0,2.0").toLong())
    }

    @Test
    fun noReconnectTurnsTheLocationOffAfterTheGrace() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        ops.clientGone(host)
        clock.advance(Location.GRACE_MS - 1)
        assertFalse(device.shells.any { it.contains("remove-test-provider") })
        clock.advance(1)
        assertTrue(device.shells.last(), device.shells.last().contains("remove-test-provider gps"))
        val n = device.shells.size
        clock.advance(10_000)
        assertEquals(n, device.shells.size)
    }

    @Test
    fun exitRemovesTheProviders() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        assertTrue(call("""{"id":2,"op":"exit"}""").getBoolean("bye"))
        assertTrue(device.shells.last(), device.shells.last().contains("remove-test-provider fused"))
        assertEquals(1, device.exits)
        val n = device.shells.size
        clock.advance(5_000)
        assertEquals(n, device.shells.size)
    }

    @Test
    fun aQueuedRepeatSendsNoStalePoint() {
        inline = false
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        clock.advance(1_000)
        assertEquals(1, queued.size)
        call("""{"id":2,"op":"location","lat":3,"lon":4}""")
        queued.removeAt(0).run()
        assertEquals("no repeat of the old point", 1, fixes("1.0,2.0"))
        assertEquals(1, fixes("3.0,4.0"))

        clock.advance(1_000)
        assertEquals(1, queued.size)
        call("""{"id":3,"op":"location","off":true}""")
        val n = device.shells.size
        queued.removeAt(0).run()
        assertEquals("no fix after off", n, device.shells.size)
        assertEquals(1, fixes("3.0,4.0"))
    }

    @Test
    fun noProviderIsAnError() {
        device.answer = { cmd ->
            if (cmd.contains("set-test-provider-enabled")) ShellResult(255, "java.lang.SecurityException: no\n", false)
            else ShellResult(0, "", false)
        }
        val r = call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        assertFalse(r.getBoolean("ok"))
        assertTrue(r.getString("error"), r.getString("error").contains("test location providers"))
        assertTrue(device.shells.last(), device.shells.last().contains("remove-test-provider gps"))
        val n = device.shells.size
        clock.advance(3_000)
        assertEquals(n, device.shells.size)
    }

    @Test
    fun aFailedFixFailsTheRequestAndRemovesWhatItAdded() {
        device.answer = { cmd ->
            if (cmd.contains("set-test-provider-location")) ShellResult(255, "java.lang.IllegalArgumentException: unknown provider\n", false)
            else ShellResult(0, "", false)
        }
        val r = call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        assertFalse(r.getBoolean("ok"))
        assertTrue(r.getString("error"), r.getString("error").contains("unknown provider"))
        assertTrue(device.shells.last(), device.shells.last().contains("remove-test-provider gps"))
        val n = device.shells.size
        clock.advance(3_000)
        call("""{"id":2,"op":"location","off":true}""")
        assertEquals("nothing left to remove", n, device.shells.size)
    }

    @Test
    fun aFailedFixKeepsTheEarlierPoint() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        device.answer = { cmd ->
            if (cmd.contains("--location 3.0,4.0")) ShellResult(255, "java.lang.IllegalArgumentException: no\n", false)
            else ShellResult(0, "", false)
        }
        assertFalse(call("""{"id":2,"op":"location","lat":3,"lon":4}""").getBoolean("ok"))
        assertFalse(device.shells.any { it.contains("remove-test-provider") })
        clock.advance(1_000)
        assertEquals(2, fixes("1.0,2.0"))
    }

    @Test
    fun helloSaysWhetherAPointIsOn() {
        assertFalse(JSONObject(ops.hello()).getBoolean("location"))
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        assertTrue(JSONObject(ops.hello()).getBoolean("location"))
        ops.clientGone(host)
        assertTrue("on through the grace", JSONObject(ops.hello()).getBoolean("location"))
        call("""{"id":2,"op":"location","off":true}""", Any())
        assertFalse(JSONObject(ops.hello()).getBoolean("location"))
    }
}
