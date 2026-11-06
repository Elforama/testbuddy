package dev.testbuddy.agent

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Test

class ReadProfileTest {
    @Test
    fun phasesAndLocalWorkAreReportedWithoutOverlap() {
        var now = 0L
        val p = ReadProfile { now }
        p.measure(ReadProfile.Part.WINDOWS) { now += 2_000_000 }
        try {
            p.measure(ReadProfile.Part.ROOTS) { now += 3_000_000; error("gone") }
        } catch (_: IllegalStateException) {}
        repeat(2) { p.measure(ReadProfile.Part.CHILDREN) { now += 1_000_000 } }
        now += 4_000_000
        p.nodes = 3
        val j = Json().obj()
        p.write(j)
        val result = JSONObject(j.endObj().toString()).getJSONObject("profile")
        assertEquals(11.0, result.getDouble("totalMs"), 0.0)
        assertEquals(4.0, result.getDouble("walkEncodeMs"), 0.0)
        assertEquals(3.0, result.getDouble("rootsMs"), 0.0)
        assertEquals(2, result.getInt("childrenCalls"))
        assertEquals(3, result.getInt("nodes"))
    }
}
