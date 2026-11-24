package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import java.lang.reflect.InvocationTargetException

class ProtocolTest {
    @Test
    fun nodeFlagsMatchTheHost() {
        val inHostOrder = listOf(
            Tree.Flag.CLICKABLE, Tree.Flag.ENABLED, Tree.Flag.FOCUSED, Tree.Flag.SCROLLABLE,
            Tree.Flag.CHECKED, Tree.Flag.SELECTED, Tree.Flag.CHECKABLE, Tree.Flag.PASSWORD,
            Tree.Flag.EDITABLE, Tree.Flag.LONG_CLICKABLE, Tree.Flag.FOCUSABLE, Tree.Flag.SHOWING_HINT,
            Tree.Flag.VISIBLE, Tree.Flag.MORE_AFTER, Tree.Flag.MORE_BEFORE,
        )
        inHostOrder.forEachIndexed { i, f -> assertEquals("flag $i", 1 shl i, f) }
    }

    @Test
    fun causeUnwrapsReflection() {
        val inner = IllegalStateException("no focused field")
        assertSame(inner, cause(InvocationTargetException(inner)))
        val plain = RuntimeException("plain")
        assertSame(plain, cause(plain))
    }
}

class ToastsTest {
    @Test
    fun showingUntilItsTimeIsUp() {
        Toasts.texts = listOf("Saved")
        Toasts.until = 1_000L
        assertEquals(listOf("Saved"), Toasts.showing())
        Toasts.until = 0L
        assertTrue(Toasts.showing().isEmpty())
        assertTrue(Toasts.SHOWN_MS >= 3_500L)
    }
}
