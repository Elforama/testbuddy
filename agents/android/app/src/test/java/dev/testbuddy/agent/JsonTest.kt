package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Test

/** The agent's replies are written by [Json]: tb reads every screen through it, so a bad escape
 *  or a misplaced comma breaks every read. */
class JsonTest {
    private val bs = '\\'

    @Test
    fun objectsArraysAndCommas() {
        val j = Json().obj().put("id", 7).put("ok", true).put("x", 1.5)
        j.key("windows").arr().obj().put("t", "a").endObj().obj().endObj().endArr()
        j.key("none").str(null).endObj()
        assertEquals("{\"id\":7,\"ok\":true,\"x\":1.5,\"windows\":[{\"t\":\"a\"},{}],\"none\":null}", j.toString())
    }

    @Test
    fun escapes() {
        val lineSeparator = 0x2028.toChar() // some JSON readers take it for a line break
        val j = Json().obj().put("t", "q\" b$bs n\n r\r t\t bell${0x07.toChar()} ls$lineSeparator é").endObj()
        val want = "{\"t\":\"q$bs\" b$bs$bs n${bs}n r${bs}r t${bs}t bell${bs}u0007 ls${bs}u2028 é\"}"
        assertEquals(want, j.toString())
    }

    @Test
    fun resetStartsOver() {
        val j = Json().obj().put("a", 1).endObj()
        j.reset().arr().num(1L).num(2).bool(false).endArr()
        assertEquals("[1,2,false]", j.toString())
    }

    @Test
    fun rawEmbedsEncodedJson() {
        val j = Json().obj().key("nodes").raw("[1,2]").put("after", 1).endObj()
        assertEquals("{\"nodes\":[1,2],\"after\":1}", j.toString())
    }
}
