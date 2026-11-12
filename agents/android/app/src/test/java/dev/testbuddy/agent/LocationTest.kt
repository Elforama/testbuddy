package dev.testbuddy.agent

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The mock location the agent keeps on the shell's test providers (issue #72): the host sends
 * one "location" request per point, and the agent sends the fix again once a second on its own,
 * off the request path, until the host turns it off or the connection that set it closes.
 */
class LocationTest {
    private val clock = FakeClock(1_000)
    private val probe = FakeProbe()
    private val events = Events(probe, clock)
    private val nodes = FakeNodes()
    private val tree = Tree(nodes) { clock.now }
    private val input = Input(FakeInjector(), clock) { nodes.field }
    private val device = FakeDevice()
    /** Background work runs inline, or waits in [queued] for the test to run it. */
    private var inline = true
    private val queued = ArrayList<Runnable>()
    private val ops = Ops(device, tree, input, events, clock) { if (inline) it.run() else queued += it }
    private val host = Any() // the connection that sends the requests

    private fun call(req: String, client: Any = host): JSONObject = JSONObject(ops.handle(req, client)!!.text)

    private fun fixes(at: String) = device.shells.count { it.contains("set-test-provider-location") && it.contains("--location $at ") }

    // issue #72: one request sets the point; the fixes that follow come from the agent itself.
    @Test
    fun oneRequestKeepsTheFixComingEverySecond() {
        val r = call("""{"id":1,"op":"location","lat":45.5,"lon":-73.5}""")
        assertTrue(r.toString(), r.getBoolean("ok"))
        assertEquals("[\"gps\",\"network\",\"fused\"]", r.getJSONArray("providers").toString())
        for (p in listOf("gps", "network", "fused")) {
            assertTrue(device.shells.toString(), device.shells.any { it == "cmd location providers add-test-provider $p" })
            assertTrue(device.shells.any { it == "cmd location providers set-test-provider-enabled $p true" })
        }
        // Listed for the host to remove if the agent is killed with them on: before any is
        // added, so a kill while adding them leaves none unlisted.
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

    // issue #72: travel moves the point request by request; the repeats carry the latest point,
    // and the providers are added once.
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

    // issue #72: the repeats end, and the providers go, when the host turns the location off...
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
        // Off again (Close after a reconnect to a new agent): nothing to remove.
        call("""{"id":3,"op":"location","off":true}""")
        assertEquals(n, device.shells.size)
    }

    // A dropped connection keeps the point for the grace: the repeats go on, and the driver's new
    // connection sets the point again and owns it from then on; the old connection's grace then
    // ends nothing (issue #72).
    @Test
    fun aReconnectWithinTheGraceKeepsThePoint() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        ops.clientGone(Any()) // another connection: not the one that set it
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

    // With no new connection (tb ended or was killed), the location goes off once the grace is
    // over: the repeats end and the providers go.
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

    // The agent exiting on its own ("exit": tb release, Release at the end of a run) takes the
    // providers away with it.
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

    // A repeat already handed to the background thread when a new point or off comes: it sends
    // nothing stale (issue #72).
    @Test
    fun aQueuedRepeatSendsNoStalePoint() {
        inline = false
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        clock.advance(1_000) // the tick fires: its repeat waits in the queue
        assertEquals(1, queued.size)
        call("""{"id":2,"op":"location","lat":3,"lon":4}""")
        queued.removeAt(0).run()
        assertEquals("no repeat of the old point", 1, fixes("1.0,2.0"))
        assertEquals(1, fixes("3.0,4.0"))

        clock.advance(1_000) // the new point's tick
        assertEquals(1, queued.size)
        call("""{"id":3,"op":"location","off":true}""")
        val n = device.shells.size
        queued.removeAt(0).run()
        assertEquals("no fix after off", n, device.shells.size)
        assertEquals(1, fixes("3.0,4.0"))
    }

    // A device where no test provider can be enabled (no mock location for the shell): an error,
    // nothing repeats, and the providers the request added are taken away again.
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

    // A fix that fails fails the request, and removes the providers that request added: nothing
    // would remove them later, with no point set (review of issue #72).
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

    // A failed fix for a new point keeps the earlier point's providers and repeats.
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

    // The greeting says whether a point is on: a host that did not set it (the run after a killed
    // one, within the grace) turns it off when it connects (issue #72, second review).
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
