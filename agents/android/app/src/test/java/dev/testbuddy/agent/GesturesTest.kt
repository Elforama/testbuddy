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
    private val events = Events(FakeProbe(), clock)

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

    /**
     * Issue #358: a press-and-hold drag. ItemTouchHelper, Compose's
     * detectDragGesturesAfterLongPress and a long-click drag and drop lift the item only once the
     * finger rested past the long-press timeout, then follow its moves: one gesture (one DOWN
     * time), DOWN, still for the hold, even moves over the duration, a still rest at the end
     * with a move each frame, UP there.
     */
    @Test
    fun holdDragPressesHoldsMovesEvenlyRestsAndLiftsInOneGesture() {
        val sentAt = ArrayList<Long>()
        injector.onTouch = { sentAt += clock.now }
        input.holdDrag(100f, 200f, 100f, 520f, holdMs = 800, durationMs = 1000)
        val down = touches.first()
        val up = touches.last()
        assertEquals(MotionEvent.ACTION_DOWN, down.action)
        assertEquals(MotionEvent.ACTION_UP, up.action)
        assertTrue("all MOVEs between", touches.drop(1).dropLast(1).all { it.action == MotionEvent.ACTION_MOVE })
        assertTrue("one gesture", touches.all { it.downAt == down.downAt })
        assertEquals(10_000L, down.at)
        assertTrue("the DOWN is waited for", injector.touches.first().sync)
        val moves = touches.filter { it.action == MotionEvent.ACTION_MOVE }
        val first = moves.first()
        assertEquals("still for the hold: nothing between the DOWN and the first move's frame", 10_800L + Input.FRAME_MS, first.at)
        val glide = moves.filter { it.at <= 11_800L }
        assertEquals("a move a frame over the duration", 1000 / Input.FRAME_MS.toInt(), glide.size)
        val dys = glide.zipWithNext { a, b -> b.y - a.y }
        assertTrue("even steps, down the line: $dys", dys.all { it > 0f && Math.abs(it - dys.first()) < 0.01f })
        assertTrue("each step at most 20 dp (40 px)", dys.all { it <= 40f } && first.y - 200f <= 40f)
        assertTrue("straight", touches.all { it.x == 100f })
        assertEquals("the move ends on the drop point", 520f, glide.last().y)
        val rest = moves.filter { it.at > glide.last().at }
        assertEquals("a move each frame of the rest", (Input.HOLD_DRAG_DROP_MS / Input.FRAME_MS).toInt(), rest.size)
        assertEquals("the settle, 6 dp (12 px) past the drop and back, then still there",
            listOf(524f, 528f, 532f, 528f, 524f) + List(rest.size - 5) { 520f }, rest.map { it.y })
        assertEquals("lifted at the drop point", 520f, up.y)
        assertEquals("after a 300 ms rest", Input.HOLD_DRAG_DROP_MS, up.at - glide.last().at)
        assertEquals("the whole gesture: hold, move, rest", 800L + 1000L + 300L, up.at - down.at)
        assertTrue("the UP is waited for", injector.touches.last().sync)
        assertTrue("no event is stamped after it is sent", touches.zip(sentAt).all { (t, s) -> t.at <= s })
        assertEquals("event times only go forward", touches.map { it.at }.sorted(), touches.map { it.at })
    }

    /**
     * Issue #358: a drop right on a row's centre is a tie for a stock ItemTouchHelper, which
     * swaps only once the dragged row's edge is strictly past the other's: on the bench, A
     * dragged onto C's centre ended at curY 294 = C's top in 2 runs of 5 (no swap, BACDE) and
     * 295 in the others. The settle goes past the drop point along the move, whichever way it
     * goes, never past the start on a short drag, and comes back before the UP.
     */
    @Test
    fun holdDragSettlesPastTheDropPointAndBack() {
        input.holdDrag(300f, 500f, 100f, 500f, holdMs = 800, durationMs = 400) // leftwards
        val end = touches.indexOfLast { it.action == MotionEvent.ACTION_MOVE && it.at <= 10_000L + 800 + 400 }
        val rest = touches.subList(end + 1, touches.size - 1)
        assertEquals(listOf(96f, 92f, 88f, 92f, 96f, 100f), rest.take(6).map { it.x })
        assertTrue("then still at the drop", rest.drop(5).all { it.x == 100f && it.y == 500f })
        assertEquals(100f, touches.last().x)

        injector.touches.clear()
        input.holdDrag(0f, 0f, 0f, 5f, holdMs = 1, durationMs = 100) // shorter than the settle
        val farthest = touches.maxOf { it.y }
        assertEquals("the settle goes no farther past than the drag is long", 10f, farthest)
        assertEquals(5f, touches.last().y)
    }

    /** Issue #358: a long, quick drag still goes in steps of at most 20 dp; a hold of 1 ms barely waits. */
    @Test
    fun holdDragStepsAreShortOnALongQuickMove() {
        input.holdDrag(0f, 0f, 0f, 2000f, holdMs = 1, durationMs = 100)
        val all = touches.map { it.y }
        val ys = all.take(all.indexOf(2000f) + 1) // up to the drop point, before the settle
        assertTrue("steps at most 40 px", ys.zipWithNext { a, b -> b - a }.all { it in 0f..40f })
        assertEquals("ends at the drop", 2000f, touches.last().y)
        val firstMove = touches[1]
        assertEquals("the first step a frame after the 1 ms hold", 1L + Input.FRAME_MS, firstMove.at - touches[0].at)
    }

    /**
     * Issue #358 review: a long drag with a short duration is slowed to a frame a step. Moves
     * sent within one frame reach the app batched, one jump of more than 20 dp, and a reorder
     * that swaps a row at a time (Collections.swap in onMove) would skip a row.
     */
    @Test
    fun holdDragLongAndQuickStillGoesAFrameAStep() {
        input.holdDrag(0f, 0f, 0f, 2000f, holdMs = 800, durationMs = 100)
        val glide = touches.filter { it.action == MotionEvent.ACTION_MOVE }.takeWhile { it.y < 2000f } +
            touches.first { it.action == MotionEvent.ACTION_MOVE && it.y == 2000f }
        assertEquals("2000 px at most 40 px a step: 50 steps", 50, glide.size)
        assertTrue("at most 20 dp apart", (listOf(touches[0]) + glide).zipWithNext { a, b -> b.y - a.y }.all { it in 0f..40f })
        assertTrue("a frame apart", glide.zipWithNext { a, b -> b.at - a.at }.all { it >= Input.FRAME_MS })
        assertEquals("the move takes 50 frames, not 100 ms", 50 * Input.FRAME_MS, glide.last().at - (touches[0].at + 800))
    }

    /** Issue #358: a DOWN the platform refuses ends the drag there, with an error: nothing more is sent. */
    @Test
    fun holdDragStopsWhenTheDownIsRefused() {
        injector.accept = { t, _ -> t.action != MotionEvent.ACTION_DOWN }
        val e = runCatching { input.holdDrag(10f, 10f, 10f, 300f, 800, 1000) }.exceptionOrNull()
        assertTrue("$e", e is IllegalStateException && e.message!!.contains("nothing was dragged"))
        assertEquals(listOf(MotionEvent.ACTION_DOWN), touches.map { it.action })
        assertEquals("no hold either", 10_000L, clock.now)
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

    /**
     * Issue #89: on a slow emulator each injection that waits for the app to handle it returns
     * late. A click-counting double tap (the bench's doubletap_box) needs the second click within
     * 300 ms of the first, and a gesture detector the second DOWN within 300 ms of the first UP.
     * The taps' spacing must come from the agent's fixed schedule, not from how long the app
     * takes to answer: nothing between the first UP and the second UP waits for it.
     */
    @Test
    fun doubleTapSpacingDoesNotStretchWhenTheAppIsSlowToTakeEvents() {
        injector.onSyncTouch = { clock.advance(150) } // each waited-for event costs 150 ms
        input.doubleTap(50f, 60f)
        val (down1, up1, down2, up2) = touches
        val gap = down2.at - up1.at
        assertTrue("gap $gap ms between the first UP and the second DOWN: past 40, inside 300", gap in 41..299)
        val clicks = up2.at - up1.at
        assertTrue("$clicks ms between the two taps' UPs: inside the 300 ms double-tap window", clicks < 300)
        assertEquals("each contact is held for its fixed time", listOf(100L, 100L), listOf(up1.at - down1.at, up2.at - down2.at))
        assertEquals("only the last UP waits for the app", listOf(false, false, false, true), injector.touches.map { it.sync })
    }

    /**
     * Issue #144: on a loaded emulator the agent's own sleeps return late (a 100 ms sleep took
     * 191 ms), and each late sleep between the taps reached the app: the bench's doubletap_box
     * counts clicks on its main thread and wants the second within 300 ms of the first, and a
     * GestureDetector the second DOWN within 300 ms of the first, both in real time. The taps'
     * contacts and gap are in the events' times; in real time only one sleep may separate the
     * taps. The events' times stay exact however late it returns: 100 ms contacts (the Flutter
     * bench's reaction row wants 75 ms, and its UPs within 350 ms), an 80 ms gap.
     */
    @Test
    fun doubleTapRealTimeSpacingSurvivesSleepsThatReturnLate() {
        clock.oversleep = 100 // every sleep returns 100 ms late
        val sentAt = ArrayList<Long>()
        injector.onTouch = { sentAt += clock.now }
        input.doubleTap(50f, 60f)
        val (down1, up1, down2, up2) = touches
        val (sentDown1, sentUp1, sentDown2, sentUp2) = sentAt
        assertTrue("${sentUp2 - sentUp1} ms between sending the two UPs (the app's two clicks): inside 300", sentUp2 - sentUp1 < 300)
        assertTrue("${sentDown2 - sentDown1} ms between sending the two DOWNs: inside 300", sentDown2 - sentDown1 < 300)
        assertEquals("stamped contact, gap, contact", listOf(100L, 80L, 100L), listOf(up1.at - down1.at, down2.at - up1.at, up2.at - down2.at))
        assertTrue("no event is stamped after it is sent", touches.zip(sentAt).all { (t, s) -> t.at <= s })
        assertEquals("event times only go forward", touches.map { it.at }.sorted(), touches.map { it.at })
    }

    /**
     * Issue #144 review: Flutter's onDoubleTap starts a 40 ms timer when it handles the first
     * DOWN, and a second DOWN handled before it fires reads as a new first tap. The second
     * contact goes in a real-time pause after the first, with room for a busy UI thread.
     */
    @Test
    fun doubleTapPausesInRealTimeBetweenTheContacts() {
        val sentAt = ArrayList<Long>()
        injector.onTouch = { sentAt += clock.now }
        input.doubleTap(50f, 60f)
        val (sentDown1, _, sentDown2, _) = sentAt
        assertTrue("${sentDown2 - sentDown1} ms between sending the two DOWNs: at least 120", sentDown2 - sentDown1 >= 120)
        assertTrue("no event is stamped after it is sent", touches.zip(sentAt).all { (t, s) -> t.at <= s })
    }

    @Test
    fun eraseMovesTheCursorToTheEndThenDeletesBackwards() {
        field = FakeField("hello", selectionStart = 1, selectionEnd = 1)
        input.erase(3, events)
        assertEquals(listOf(5 to 5), field!!.selects)
        assertEquals(List(3) { KeyEvent.KEYCODE_DEL }, keyCodes(KeyEvent.ACTION_DOWN))
        assertEquals(List(3) { KeyEvent.KEYCODE_DEL }, keyCodes(KeyEvent.ACTION_UP))
        assertTrue("only the last key waits", injector.keys.last().sync)
        assertEquals(1, injector.keys.count { it.sync })
    }

    /** Issue #289: a caret reported at the end is set there all the same: a Compose field reported that while its caret was mid-text. */
    @Test
    fun eraseSetsACaretReportedAtTheEnd() {
        field = FakeField("hello")
        input.erase(2, events)
        assertEquals(listOf(5 to 5), field!!.selects)
        assertEquals(List(2) { KeyEvent.KEYCODE_DEL }, keyCodes(KeyEvent.ACTION_DOWN))
    }

    @Test
    fun eraseInAFieldShowingItsHintTreatsItAsEmpty() {
        field = FakeField("Email", showingHint = true, selectionStart = 5, selectionEnd = 5)
        input.erase(1, events)
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
            input.erase(1, events)
            assertEquals("field $f", listOf(KeyEvent.KEYCODE_MOVE_END, KeyEvent.KEYCODE_DEL), keyCodes(KeyEvent.ACTION_DOWN))
        }
    }

    @Test
    fun eraseCountIsBounded() {
        field = FakeField("x")
        input.erase(-4, events)
        assertTrue(keyCodes(KeyEvent.ACTION_DOWN).isEmpty())
        input.erase(50_000, events)
        assertEquals(10_000, keyCodes(KeyEvent.ACTION_DOWN).size)
    }
}
