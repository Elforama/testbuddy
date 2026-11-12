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
    private val ops = Ops(device, tree, input, events, clock)
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
        val n = device.shells.size
        clock.advance(5_000)
        assertEquals(device.shells.drop(n).toString(), n, device.shells.size)
        // Off again (Close after a reconnect to a new agent): nothing to remove.
        call("""{"id":3,"op":"location","off":true}""")
        assertEquals(n, device.shells.size)
    }

    // ...and when the connection that set it closes: tb ended, was killed, or lost the link. A
    // driver that reconnects sends the point again on its new connection.
    @Test
    fun theSettingConnectionClosingStopsTheRepeats() {
        call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        ops.clientGone(Any()) // another connection: not the one that set it
        clock.advance(1_000)
        assertEquals(2, fixes("1.0,2.0"))
        ops.clientGone(host)
        assertTrue(device.shells.last().contains("remove-test-provider gps"))
        val n = device.shells.size
        clock.advance(5_000)
        assertEquals(n, device.shells.size)

        val next = Any()
        call("""{"id":1,"op":"location","lat":1,"lon":2}""", next)
        assertTrue(device.shells.drop(n).any { it == "cmd location providers add-test-provider gps" })
        clock.advance(1_000)
        assertEquals(4, fixes("1.0,2.0"))
        ops.clientGone(host) // the old connection's end noticed late: the new one keeps it
        clock.advance(1_000)
        assertEquals(5, fixes("1.0,2.0"))
    }

    // A device where no test provider can be added (no mock location for the shell): an error,
    // and nothing repeats.
    @Test
    fun noProviderIsAnError() {
        device.answer = { cmd ->
            if (cmd.contains("set-test-provider-enabled")) ShellResult(255, "java.lang.SecurityException: no\n", false)
            else ShellResult(0, "", false)
        }
        val r = call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        assertFalse(r.getBoolean("ok"))
        assertTrue(r.getString("error"), r.getString("error").contains("test location providers"))
        val n = device.shells.size
        clock.advance(3_000)
        assertEquals(n, device.shells.size)
    }

    // A fix that fails (the providers were taken away under it) fails the request.
    @Test
    fun aFailedFixFailsTheRequest() {
        device.answer = { cmd ->
            if (cmd.contains("set-test-provider-location")) ShellResult(255, "java.lang.IllegalArgumentException: unknown provider\n", false)
            else ShellResult(0, "", false)
        }
        val r = call("""{"id":1,"op":"location","lat":1,"lon":2}""")
        assertFalse(r.getBoolean("ok"))
        assertTrue(r.getString("error"), r.getString("error").contains("unknown provider"))
    }
}
