package dev.testbuddy.agent

import android.view.KeyEvent
import android.view.MotionEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
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
        assertNull(input.typeChecked("abc", events, waitForFocus = true))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L + Input.FOCUS_WAIT_MS, clock.now) // CI mode: waited for focus to arrive (issue #97)
    }

    /** Outside CI mode the host does not ask for the focus wait: no field, no wait, as before issue #97. */
    @Test
    fun checkedTypingOutsideCIDoesNotWaitForFocus() {
        assertNull(input.typeChecked("abc", events, waitForFocus = false))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L, clock.now)
    }

    @Test
    fun checkedTypingRemovesAnExactDuplicatedPrefixInTheOriginalNativeEditor() {
        val editor = FakeField(nativePlainText = true)
        field = editor
        injector.onKey = {
            if (it.action == KeyEvent.ACTION_UP) when (it.code) {
                KeyEvent.KEYCODE_B -> editor.value = "b"
                KeyEvent.KEYCODE_Y -> {
                    editor.value = "buddbuddy"
                    editor.selectionStart = editor.value.length
                    editor.selectionEnd = editor.value.length
                }
            }
        }
        assertTrue(input.typeChecked("buddy", events)!!)
        assertEquals("buddy", editor.value)
        assertEquals(5, editor.selectionStart)
    }

    @Test
    fun checkedTypingBoundsRetriesForMaskedOrFormattedFields() {
        field = FakeField("hidden")
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals(12_000L, clock.now)
        assertEquals(listOf(KeyEvent.KEYCODE_A, KeyEvent.KEYCODE_A, KeyEvent.KEYCODE_B, KeyEvent.KEYCODE_C),
            injector.keys.filter { it.event.action == KeyEvent.ACTION_UP }.map { it.event.code })
    }

    /**
     * Issue #97: an Enter whose editor action moves focus to the next field returns once the
     * view has focus, before the keyboard's connection has moved there; a key typed meanwhile
     * is dropped and the field stays empty. A one-character text was typed unchecked, so
     * `inputText: "c"` reported success with nothing typed. The key must be checked and sent
     * again once the field takes keys. The connection moves 800 ms after the Enter, on the
     * fake clock.
     */
    @Test
    fun checkedTypingOfOneCharacterRetypesAKeyLostWhileFocusIsStillMoving() {
        val third = FakeField(nativePlainText = true)
        field = third
        var connected = false
        clock.postDelayed({ connected = true }, 800)
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && connected) third.value += letter(it) }
        assertTrue(input.typeChecked("c", events)!!)
        assertEquals("c", third.value)
        assertEquals(2, injector.keys.count { it.event.action == KeyEvent.ACTION_UP })
    }

    /** Issue #97: when the focused editor never takes the text, typing fails and says so. */
    @Test
    fun checkedTypingFailsWhenTheFocusedEditorNeverTakesTheText() {
        field = FakeField(nativePlainText = true)
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("c", events) }
        assertTrue(e.message, e.message!!.contains("no field took the text"))
    }

    /**
     * Issue #97: with no editor focused yet (focus still moving), typing in CI mode waits for one
     * first. Outside CI it does not wait: nothing is typed, and the host uses its own guard.
     */
    @Test
    fun checkedTypingWaitsForAnEditorToTakeFocusBeforeSendingKeys() {
        val third = FakeField(nativePlainText = true)
        clock.postDelayed({ field = third }, 300)
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && field === third) third.value += letter(it) }
        assertTrue(input.typeChecked("c", events, waitForFocus = true)!!)
        assertEquals("c", third.value)
        assertEquals(10_300L, injector.keys.first().event.at)
    }

    @Test
    fun checkedTypingOutsideCIHandsAFieldNotFocusedYetToTheHost() {
        val third = FakeField(nativePlainText = true)
        clock.postDelayed({ field = third }, 300)
        assertNull(input.typeChecked("c", events, waitForFocus = false))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L, clock.now)
    }

    /**
     * Issue #97: a plain field whose filter rejects the first character but takes the rest
     * (a digits-only field, a filter that drops "+") must not fail the step: only a field that
     * took nothing of the text does.
     */
    @Test
    fun checkedTypingIntoAFieldThatRejectsOnlyTheFirstCharacterSucceeds() {
        val filtered = FakeField(nativePlainText = true)
        field = filtered
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && it.code != KeyEvent.KEYCODE_X) filtered.value += letter(it) }
        assertTrue(input.typeChecked("xab", events)!!)
        assertEquals("ab", filtered.value)
    }

    private fun letter(k: Key) = 'a' + (k.code - KeyEvent.KEYCODE_A)

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
    fun doubleTapReachesControlsThatRequireContactAcrossFrames() {
        input.doubleTap(5f, 5f)
        assertEquals(listOf(
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP,
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP,
        ), actions)
        val events = injector.touches.map { it.event }
        val holds = events.chunked(2).map { (down, up) -> up.at - down.at }
        assertTrue("each contact must reach the reaction control: $holds", holds.all { it >= 75 })
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

    // AND-006: an IME can consume hardware Enter to dismiss itself without
    // submitting. The editor's advertised action must be delivered once instead.
    @Test
    fun enterUsesTheEditorsSubmitActionWithoutInjectingAnotherKey() {
        var submissions = 0
        val editor = object : Field by FakeField(value = "hello") {
            override fun submitIme(): Boolean { submissions++; return true }
        }
        val input = Input(injector, clock) { editor }
        input.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(1, submissions)
        assertTrue("hardware Enter can be consumed or submit twice", injector.keys.isEmpty())
    }

    @Test
    fun unsupportedSubmitAndLongEnterKeepHardwareKeySemantics() {
        var attempts = 0
        val editor = object : Field by FakeField(value = "hello") {
            override fun submitIme(): Boolean { attempts++; return false }
        }
        val input = Input(injector, clock) { editor }
        input.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(1, attempts)
        assertEquals(listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP), injector.keys.map { it.event.action })
        input.key(KeyEvent.KEYCODE_ENTER, longPress = true)
        assertEquals("long presses must not turn into editor actions", 1, attempts)
        assertEquals(5, injector.keys.size)
    }

    // Issue #32: a multi-line Compose field advertises the IME action and does not report
    // isMultiLine, so only its keyboard settings tell Enter to type a new line. Performing the
    // action there typed nothing.
    private fun advertising(pkg: String = "dev.app", onSubmit: () -> Unit) = object : Field by FakeField(value = "one") {
        override fun submitIme(): Boolean { onSubmit(); return true }
        override val packageName: CharSequence? get() = pkg
    }

    @Test
    fun enterInAMultiLineEditorTypesANewLineInsteadOfTheEditorAction() {
        var submissions = 0
        var reads = 0
        val editor = advertising { submissions++ }
        val input = Input(injector, clock, { reads++; EditorSettings("dev.app", 0x28001, 0x42000000) }) { editor }
        assertEquals("hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals("the IME action types nothing in a multi-line field", 0, submissions)
        assertEquals(listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP), injector.keys.map { it.event.action })
        assertEquals(1, reads)
    }

    @Test
    fun enterInASingleLineEditorStillRunsItsActionOnce() {
        var submissions = 0
        val editor = advertising { submissions++ }
        val input = Input(injector, clock, { EditorSettings("dev.app", 0x8001, 0x2000006) }) { editor }
        assertEquals("editor-action", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals(1, submissions)
        assertTrue("AND-006: no key after a successful action", injector.keys.isEmpty())
    }

    @Test
    fun anotherAppsOrUnknownKeyboardSettingsAreNotTrusted() {
        var submissions = 0
        val editor = advertising { submissions++ }
        Input(injector, clock, { EditorSettings("other.app", 0x28001, 0x42000000) }) { editor }.key(KeyEvent.KEYCODE_ENTER)
        Input(injector, clock, { null }) { editor }.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(2, submissions)
        assertTrue(injector.keys.isEmpty())
    }

    @Test
    fun keyboardSettingsAreReadOnlyWhenEnterCouldBeAnEditorAction() {
        var reads = 0
        val settings = { reads++; EditorSettings("dev.app", 0x28001, 0x42000000) }
        val cannot = object : Field by FakeField(value = "one") {
            override fun canSubmitIme() = false
        }
        Input(injector, clock, settings) { cannot }.key(KeyEvent.KEYCODE_ENTER)
        Input(injector, clock, settings) { advertising {} }.key(KeyEvent.KEYCODE_ENTER, longPress = true)
        Input(injector, clock, settings) { null }.key(KeyEvent.KEYCODE_ENTER)
        Input(injector, clock, settings) { advertising {} }.key(KeyEvent.KEYCODE_TAB)
        assertEquals("dumpsys costs ~150 ms: not for keys that cannot become an action", 0, reads)
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
