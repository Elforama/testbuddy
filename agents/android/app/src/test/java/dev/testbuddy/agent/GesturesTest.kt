package dev.testbuddy.agent

import android.view.KeyEvent
import android.view.MotionEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Long presses, double taps and erasing: the gestures' timing and the keys erasing sends. */
class GesturesTest {
    private val clock = FakeClock(10_000)
    private val injector = FakeInjector(density = 2f)
    private var field: FakeField? = null
    private val input = Input(injector, clock) { field }

    private val touches get() = injector.touches.map { it.event }
    private fun keyCodes(action: Int) = injector.keys.filter { it.event.action == action }.map { it.event.code }

    @Test
    fun pressHoldsTheContactForTheWholeHoldInOneGesture() {
        input.press(120f, 340f, 650)
        assertEquals(listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP), touches.map { it.action })
        val (down, up) = touches
        assertEquals(10_000L, down.downAt)
        assertEquals(10_000L, down.at)
        assertEquals("UP belongs to the same gesture", down.downAt, up.downAt)
        assertEquals(10_650L, up.at)
        assertEquals(listOf(120f to 340f, 120f to 340f), touches.map { it.x to it.y })
        assertTrue("both ends wait for the app to take them", injector.touches.all { it.sync })
    }

    @Test
    fun doubleTapIsTwoSeparateContactsInsideTheDoubleTapTimeout() {
        input.doubleTap(50f, 60f)
        assertEquals(
            listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP, MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP),
            touches.map { it.action },
        )
        val (down1, up1, down2, up2) = touches
        assertEquals("first contact", down1.downAt, up1.downAt)
        assertEquals("second contact", down2.downAt, up2.downAt)
        assertTrue("the second tap is a new gesture", down2.downAt > up1.at)
        val gap = down2.at - up1.at
        assertTrue("gap $gap ms: past the 40 ms minimum, inside the 300 ms timeout", gap in 41..299)
        assertTrue("the whole double tap fits the timeout", up2.at - down1.at < 300 + 2 * 100)
        assertTrue(touches.all { it.x == 50f && it.y == 60f })
    }

    @Test
    fun eraseMovesTheCursorToTheEndThenDeletesBackwards() {
        field = FakeField("hello", selectionStart = 1, selectionEnd = 1)
        input.erase(3)
        assertEquals(listOf(5 to 5), field!!.selects)
        assertEquals(List(3) { KeyEvent.KEYCODE_DEL }, keyCodes(KeyEvent.ACTION_DOWN))
        assertEquals(List(3) { KeyEvent.KEYCODE_DEL }, keyCodes(KeyEvent.ACTION_UP))
        assertTrue("only the last key waits", injector.keys.last().sync)
        assertEquals(1, injector.keys.count { it.sync })
    }

    @Test
    fun eraseLeavesACursorAlreadyAtTheEnd() {
        field = FakeField("hello")
        input.erase(2)
        assertTrue(field!!.selects.isEmpty())
        assertEquals(List(2) { KeyEvent.KEYCODE_DEL }, keyCodes(KeyEvent.ACTION_DOWN))
    }

    @Test
    fun eraseInAFieldShowingItsHintTreatsItAsEmpty() {
        field = FakeField("Email", showingHint = true, selectionStart = 5, selectionEnd = 5)
        input.erase(1)
        assertEquals(listOf(0 to 0), field!!.selects)
    }

    @Test
    fun eraseFallsBackToTheEndKeyWhenTheCursorCannotBeSet() {
        for (f in listOf(
            null, // nothing focused
            FakeField("abc", editable = false, selectionStart = 0, selectionEnd = 0),
            FakeField("abc", selectionStart = 0, selectionEnd = 0, acceptSelect = false),
        )) {
            injector.keys.clear()
            field = f
            input.erase(1)
            assertEquals("field $f", listOf(KeyEvent.KEYCODE_MOVE_END, KeyEvent.KEYCODE_DEL), keyCodes(KeyEvent.ACTION_DOWN))
        }
    }

    @Test
    fun eraseCountIsBounded() {
        field = FakeField("x")
        input.erase(-4)
        assertTrue(keyCodes(KeyEvent.ACTION_DOWN).isEmpty())
        input.erase(50_000)
        assertEquals(10_000, keyCodes(KeyEvent.ACTION_DOWN).size)
    }
}
