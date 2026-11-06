package dev.testbuddy.agent

import android.view.KeyEvent
import android.view.MotionEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.assertNull
import org.junit.Test

/** The touch and key events the agent's gestures and typing send, timed on a fake clock. */
class InputTest {
    private val clock = FakeClock(10_000)
    private val injector = FakeInjector(density = 2f)
    private var field: FakeField? = null
    private val input = Input(injector, clock) { field }

    private val actions get() = injector.touches.map { it.event.action }

    private val events = Events(FakeProbe(), clock)

    @Test
    fun swipeWaitsForInputWindowsBeforeSendingAndStampingDown() {
        var ready = false
        var dropped = false
        injector.onPrepareSwipe = { clock.advance(75); ready = true }
        injector.onTouch = { if (it.action == MotionEvent.ACTION_DOWN && !ready) dropped = true }
        input.swipe(0f, 100f, 0f, 500f, 400)
        assertFalse("DOWN was sent before the window could take it", dropped)
        assertEquals(10_075L, injector.touches.first().event.downAt)
        assertEquals(10_475L, injector.touches.last().event.at)
    }

    @Test
    fun checkedTypingRecognizesFocusMovingBetweenEmptyCodeBoxes() {
        field = FakeField()
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP) field = FakeField() }
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals(10_000L, clock.now)
        assertEquals(3, injector.keys.count { it.event.action == KeyEvent.ACTION_UP })
    }

    @Test
    fun checkedTypingKeepsSurrogatePairsAndSelectionIntact() {
        field = FakeField("old", selectionStart = 0, selectionEnd = 3)
        assertTrue(input.typeChecked("😀好", events)!!)
        assertEquals("😀好", field!!.value)
        assertEquals(10_000L, clock.now)
    }

    @Test
    fun checkedTypingWithoutAnObservableFieldInjectsNothingForHostFallback() {
        assertNull(input.typeChecked("abc", events))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L, clock.now)
    }

    @Test
    fun checkedTypingBoundsRetriesForMaskedOrFormattedFields() {
        field = FakeField("hidden")
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals(12_000L, clock.now)
        assertEquals(listOf(KeyEvent.KEYCODE_A, KeyEvent.KEYCODE_A, KeyEvent.KEYCODE_B, KeyEvent.KEYCODE_C),
            injector.keys.filter { it.event.action == KeyEvent.ACTION_UP }.map { it.event.code })
    }

    @Test
    fun aTapIsDownThenUpWaitedFor() {
        input.tap(100f, 200f)
        assertEquals(listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP), actions)
        assertEquals(listOf(false, true), injector.touches.map { it.sync })
        assertTrue(injector.touches.all { it.event.downAt == 10_000L && it.event.x == 100f && it.event.y == 200f })
    }

    @Test
    fun aDoubleTapFitsTheDoubleTapTimeout() {
        input.doubleTap(5f, 5f)
        val downs = injector.touches.filter { it.event.action == MotionEvent.ACTION_DOWN }.map { it.event.at }
        assertEquals(2, downs.size)
        val gap = downs[1] - downs[0]
        assertTrue("gap $gap ms", gap in 40..300)
    }

    @Test
    fun aPressHoldsForItsTime() {
        input.press(5f, 5f, 1_500)
        val (down, up) = injector.touches.map { it.event }
        assertEquals(1_500L, up.at - down.at)
        assertEquals(down.downAt, up.downAt)
    }

    @Test
    fun aSwipeMovesEveryFrameOverItsDuration() {
        input.swipe(0f, 1000f, 0f, 200f, 400)
        val moves = injector.touches.filter { it.event.action == MotionEvent.ACTION_MOVE }
        assertEquals((400 / Input.FRAME_MS).toInt(), moves.size)
        assertEquals(MotionEvent.ACTION_DOWN, actions.first())
        val up = injector.touches.last()
        assertEquals(MotionEvent.ACTION_UP, up.event.action)
        assertTrue(up.sync)
        assertEquals(200f, up.event.y)
        // Paced like a finger: the last move lands at the swipe's end.
        assertEquals(400L, moves.last().event.at - injector.touches.first().event.at)
        // Monotonic, evenly spread.
        val ys = moves.map { it.event.y }
        assertEquals(ys.sortedDescending(), ys)
    }

    @Test
    fun anUpOrDownDragEndsInACancelSoNothingFlings() {
        input.drag(500f, 1500f, 500f, 500f)
        val events = injector.touches.map { it.event }
        assertEquals(MotionEvent.ACTION_DOWN, events.first().action)
        assertEquals(MotionEvent.ACTION_CANCEL, events.last().action)
        // Stamped as a drag that ended a moment ago: DOWN back-dated, the rest in order.
        assertEquals(clock.now - 25 - (Input.DRAG_MS + Input.DRAG_REST_MS), events.first().at)
        val times = events.dropLast(1).map { it.at }
        assertEquals(times.sorted(), times)
        // At rest at the end: the last two moves at the same point.
        val moves = events.filter { it.action == MotionEvent.ACTION_MOVE }
        assertEquals(moves[moves.size - 2].y, moves.last().y)
        assertEquals(500f, moves.last().y)
        // The two first moves pass the touch slop (16 and 20 dp, at 2 px per dp), each waited for.
        assertEquals(1500f - 32f, moves[0].y)
        assertEquals(1500f - 40f, moves[1].y)
        assertTrue(injector.touches.filter { it.event.action == MotionEvent.ACTION_MOVE }.take(2).all { it.sync })
    }

    @Test
    fun aSidewaysDragLiftsSoAPagerSnaps() {
        input.drag(900f, 1000f, 100f, 1000f)
        assertEquals(MotionEvent.ACTION_UP, actions.last())
    }

    @Test
    fun aDragNeverStartsBeforeTheLastEvent() {
        input.tap(1f, 1f)
        val tappedAt = clock.now
        input.drag(500f, 1500f, 500f, 500f)
        val down = injector.touches.first { it.event.action == MotionEvent.ACTION_DOWN && it.event.downAt != tappedAt }
        assertTrue("drag DOWN at ${down.event.at}, tap at $tappedAt", down.event.at > tappedAt)
    }

    @Test
    fun aDragTooShortToMoveIsATap() {
        input.drag(10f, 10f, 10.5f, 10f)
        assertEquals(listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP), actions)
    }

    @Test
    fun aKeyIsDownThenUp() {
        input.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP), injector.keys.map { it.event.action })
        assertEquals(listOf(false, true), injector.keys.map { it.sync })
    }

    @Test
    fun aLongKeyPressRepeatsWithTheLongPressFlag() {
        input.key(KeyEvent.KEYCODE_POWER, longPress = true)
        val (down, long, up) = injector.keys.map { it.event }
        assertEquals(1, long.repeat)
        assertEquals(KeyEvent.FLAG_LONG_PRESS, long.flags)
        assertEquals(600L, long.at - down.at)
        assertEquals(KeyEvent.ACTION_UP, up.action)
        assertEquals(down.downAt, up.downAt)
    }

    @Test
    fun typingSendsKeysInChunksEachChunksLastWaitedFor() {
        val text = "abcdefghijklmnopqrstuvwxyz" // 26 keys: a chunk of 16, then 10
        assertTrue(input.type(text))
        assertEquals(52, injector.keys.size)
        val waited = injector.keys.withIndex().filter { it.value.sync }.map { it.index }
        assertEquals(listOf(Input.KEY_CHUNK * 2 - 1, 51), waited)
        assertEquals(KeyEvent.KEYCODE_A, injector.keys.first().event.code)
    }

    @Test
    fun textWithNoKeysIsSetOnTheFocusedField() {
        field = FakeField("Hello world", selectionStart = 5, selectionEnd = 5)
        assertTrue(input.type(" 😀"))
        assertEquals("Hello 😀 world", field!!.value)
        assertEquals(5 + 3 to 5 + 3, field!!.selects.last())
        assertTrue(injector.keys.isEmpty())
    }

    @Test
    fun textWithNoKeysReplacesTheSelectionAndIgnoresAHint() {
        field = FakeField("Search", showingHint = true, selectionStart = 0, selectionEnd = 0)
        assertTrue(input.type("😀"))
        assertEquals("😀", field!!.value)
        field = FakeField("abcdef", selectionStart = 4, selectionEnd = 1)
        assertTrue(input.type("é"))
        assertEquals("aéef", field!!.value)
    }

    @Test
    fun textWithNoKeysAndNoFieldFails() {
        assertFalse(input.type("😀"))
        field = FakeField("x", editable = false)
        assertFalse(input.type("😀"))
    }

    @Test
    fun eraseMovesTheCursorToTheEndFirst() {
        field = FakeField("hello", selectionStart = 2, selectionEnd = 2)
        input.erase(3)
        assertEquals(5 to 5, field!!.selects.single())
        assertEquals(List(3) { listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP) }.flatten(), injector.keys.map { it.event.action })
        assertTrue(injector.keys.all { it.event.code == KeyEvent.KEYCODE_DEL })
        assertTrue(injector.keys.last().sync)
    }

    @Test
    fun eraseFallsBackToTheEndKey() {
        field = FakeField("hello", selectionStart = 2, selectionEnd = 2, acceptSelect = false)
        input.erase(1)
        assertEquals(KeyEvent.KEYCODE_MOVE_END, injector.keys.first().event.code)
        assertEquals(KeyEvent.KEYCODE_DEL, injector.keys.last().event.code)
    }

    @Test
    fun eraseAtTheEndAlreadyMovesNothing() {
        field = FakeField("hello")
        input.erase(2)
        assertTrue(field!!.selects.isEmpty())
        assertEquals(4, injector.keys.size)
    }

    @Test
    fun clearEmptiesTheFocusedFieldInOneAction() {
        assertFalse(input.clearFocused())
        field = FakeField("secret")
        assertTrue(input.clearFocused())
        assertEquals("", field!!.value)
        assertTrue(injector.keys.isEmpty())
    }
}
