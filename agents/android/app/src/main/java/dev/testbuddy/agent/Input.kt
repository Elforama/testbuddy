package dev.testbuddy.agent

import android.view.KeyEvent
import android.view.MotionEvent

/**
 * Turns the protocol's gestures and typing into touch and key events, timed the way a finger
 * and a keyboard would send them. The events go to an [Injector] (on the device: the input
 * manager, or UiAutomation: AndroidPlatform.kt); the focused field comes from [focused].
 */
class Input(
    private val injector: Injector,
    private val clock: Clock,
    /** The focused editor's keyboard settings (EditorSettings.read), null when unknown. */
    private val editorSettings: () -> EditorSettings? = { null },
    private val focused: () -> Field?,
) {
    val path: String get() = injector.path

    /** The latest event time injected: a gesture's back-dated DOWN must not go before it. */
    private var lastEventAt = 0L

    private fun motion(down: Long, action: Int, x: Float, y: Float, sync: Boolean, at: Long = clock.uptimeMillis()) {
        lastEventAt = maxOf(lastEventAt, at)
        injector.touch(Touch(action, down, at, x, y), sync)
    }

    fun tap(x: Float, y: Float) {
        val down = clock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x, y, false, down)
        motion(down, MotionEvent.ACTION_UP, x, y, true)
    }

    /**
     * Two contacts on the agent's own fixed schedule: DOWN, UP [DOUBLE_TAP_CONTACT_MS] later,
     * the second DOWN [DOUBLE_TAP_GAP_MS] after that (inside the 300 ms double-tap timeout,
     * past the 40 ms minimum), its UP a contact later. Only the last UP waits for the app:
     * waiting on the others put the app's handling time between the taps, and on a slow
     * emulator that pushed them out of the double-tap window (issue #89).
     */
    fun doubleTap(x: Float, y: Float) {
        // Keep both contacts down across frames so press tracking can see them.
        contact(x, y, sync = false)
        clock.sleep(DOUBLE_TAP_GAP_MS)
        contact(x, y, sync = true)
    }

    private fun contact(x: Float, y: Float, sync: Boolean) {
        val down = clock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x, y, false, down)
        clock.sleep(DOUBLE_TAP_CONTACT_MS)
        motion(down, MotionEvent.ACTION_UP, x, y, sync)
    }

    fun press(x: Float, y: Float, holdMs: Long) {
        val down = clock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x, y, true, down)
        clock.sleep(holdMs) // the app's long-press timer runs in real time
        motion(down, MotionEvent.ACTION_UP, x, y, true)
    }

    /** A straight-line drag in [durationMs], a move every frame. */
    fun swipe(x1: Float, y1: Float, x2: Float, y2: Float, durationMs: Long) {
        // A point swipe has no element lookup to wait for the new input window.
        // Synchronize before stamping DOWN; a dropped DOWN loses the whole gesture.
        injector.prepareSwipe()
        val down = clock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x1, y1, false, down)
        val steps = maxOf(2, (durationMs / FRAME_MS).toInt())
        val start = clock.uptimeMillis()
        for (i in 1..steps) {
            val frac = i.toFloat() / steps
            val target = start + (durationMs * frac).toLong()
            val wait = target - clock.uptimeMillis()
            if (wait > 0) clock.sleep(wait) // gesture pacing: the app derives velocity from it
            motion(down, MotionEvent.ACTION_MOVE, x1 + (x2 - x1) * frac, y1 + (y2 - y1) * frac, false)
        }
        motion(down, MotionEvent.ACTION_UP, x2, y2, true)
    }

    /**
     * A drag that leaves the content where the finger stops, with no fling (scrollUntilVisible).
     * Its events are stamped as a [DRAG_MS] drag that ended [DRAG_REST_MS] ago, so the app sees
     * a finger's pace, but they go in at once instead of in real time. The pointer then rests
     * (velocity trackers read a still pointer as zero speed), and the gesture ends with a CANCEL
     * for up and down drags, which scroll views never fling on. Sideways ones lift, so a paging
     * list snaps to its page. The moves are all handled by the app before the end goes in.
     */
    fun drag(x1: Float, y1: Float, x2: Float, y2: Float) {
        val dx = x2 - x1
        val dy = y2 - y1
        val len = Math.hypot(dx.toDouble(), dy.toDouble()).toFloat()
        if (len < 1f) return tap(x1, y1)
        val back = DRAG_MS + DRAG_REST_MS
        // The input verifier drops a gesture whose DOWN predates the last event.
        val gap = lastEventAt - (clock.uptimeMillis() - back) + 1
        if (gap > 0) clock.sleep(gap)
        val down = clock.uptimeMillis() - back
        val ux = dx / len
        val uy = dy / len
        fun at(d: Float) = floatArrayOf(x1 + ux * minOf(d, len), y1 + uy * minOf(d, len))
        motion(down, MotionEvent.ACTION_DOWN, x1, y1, false, down)
        // Two moves just past the touch slop, each waited for: a scrolling parent takes the
        // gesture over on a move of its own (moves sent together are spent on that).
        val dp = density()
        var p = at(16 * dp)
        motion(down, MotionEvent.ACTION_MOVE, p[0], p[1], true, down + 8)
        val kick = 20 * dp
        p = at(kick)
        motion(down, MotionEvent.ACTION_MOVE, p[0], p[1], true, down + 16)
        val steps = maxOf(1, ((DRAG_MS - 16) / FRAME_STEP_MS).toInt())
        for (i in 1..steps) {
            val f = i.toFloat() / steps
            p = at(kick + (len - kick) * f)
            motion(down, MotionEvent.ACTION_MOVE, p[0], p[1], false, down + 16 + ((DRAG_MS - 16) * f).toLong())
        }
        // At rest at the end: no speed left to fling with. The last move is waited for: the app
        // batches moves until its next frame, and a CANCEL that arrives before that frame makes
        // it discard the whole batch unseen (InputConsumer skips moves it is about to cancel), so
        // the list would move only the two waited moves' worth (on a busy app, every other drag).
        motion(down, MotionEvent.ACTION_MOVE, x2, y2, false, down + DRAG_MS + 10)
        motion(down, MotionEvent.ACTION_MOVE, x2, y2, true, down + back - 5)
        clock.sleep(25) // the end lands in a later frame than the moves
        val vertical = Math.abs(dy) >= Math.abs(dx)
        motion(down, if (vertical) MotionEvent.ACTION_CANCEL else MotionEvent.ACTION_UP, x2, y2, true)
    }

    private fun density(): Float = try { injector.density } catch (_: Throwable) { 2.75f }

    /**
     * Whether the field's own keyboard settings say Enter types a new line (issue #32): a
     * multi-line Compose field advertises the IME action, which types nothing there, and does
     * not report isMultiLine. Settings from another app, or none, decide nothing.
     */
    private fun enterIsNewline(f: Field): Boolean {
        val s = editorSettings() ?: return false
        val pkg = f.packageName?.toString()
        if (s.pkg != null && pkg != null && s.pkg != pkg) return false
        return s.enterIsNewline
    }

    fun key(code: Int, longPress: Boolean = false): String {
        // AND-006: a keyboard may consume Enter only to dismiss/commit its
        // composition. Use the focused single-line editor's advertised submit
        // action instead. A successful action must never be followed by a key.
        if (code == KeyEvent.KEYCODE_ENTER && !longPress) {
            val f = focused()
            if (f != null && f.canSubmitIme() && !enterIsNewline(f) && f.submitIme()) return "editor-action"
        }
        val t = clock.uptimeMillis()
        injector.key(Key(KeyEvent.ACTION_DOWN, code, t, t), false)
        if (longPress) {
            clock.sleep(600)
            injector.key(Key(KeyEvent.ACTION_DOWN, code, t, clock.uptimeMillis(), repeat = 1, flags = KeyEvent.FLAG_LONG_PRESS), false)
        }
        injector.key(Key(KeyEvent.ACTION_UP, code, t, clock.uptimeMillis()), true)
        return "hardware"
    }

    /**
     * Types text as key presses, so the app sees what a keyboard sends. Characters with no
     * key (emoji, most non-Latin scripts) go in through ACTION_SET_TEXT on the focused field.
     */
    fun type(text: String): Boolean {
        val presses = ArrayList<List<Key>>()
        for (ch in text) {
            val events = injector.keysFor(ch)
            if (events == null) return insertText(text)
            presses += events
        }
        injectKeys(presses)
        return true
    }

    /**
     * Check the first code point at the focused field, then send the remainder. A newly
     * focused IME can lose that first key. Keep the host's one-second, one-retry guard,
     * waking on accessibility callbacks instead of pulling whole screens over the socket.
     *
     * Text of one character is checked too (issue #97): right after an Enter whose editor
     * action moved focus to the next field, the field has focus before the keyboard's
     * connection has moved to it, and a key sent then is dropped. With nothing focused yet,
     * it first waits (on accessibility events, up to [FOCUS_WAIT_MS]) for a field to take
     * focus. A native text field reports its text exactly, so when it shows no change at all
     * after the whole text (the first key retried), typing fails instead of reporting text
     * that went nowhere. A field that rejects only some characters still passes.
     */
    fun typeChecked(text: String, events: Events): Boolean? {
        if (text.isEmpty()) return type(text)
        var initial = focused()
        if (initial == null && events.awaitCondition(FOCUS_WAIT_MS) { focused() != null }) initial = focused()
        // No input yet: let the runner use its full-screen guard for custom widgets
        // whose editable accessibility node is absent from Android's focus API.
        if (initial == null) return null
        val before = fieldState(initial) ?: return null
        val blankNativeField = initial.nativePlainText && before.second.isEmpty() &&
            initial.selectionStart == 0 && initial.selectionEnd == 0
        val end = text.offsetByCodePoints(0, 1)
        val first = text.substring(0, end)
        var took = false
        for (attempt in 0..1) {
            if (!type(first)) return false
            if (events.awaitCondition(1000) { fieldState() != before }) { took = true; break }
        }
        val rest = text.substring(end)
        if (!type(rest)) return false
        // A field may reject only the first character (a filter, a digits-only field) and
        // take the rest: judge the whole text. Masked, formatted and custom fields may not
        // show what was typed: no verdict for them.
        if (!took && rest.isNotEmpty() && initial.nativePlainText) took = events.awaitCondition(1000) { fieldState() != before }
        check(took || !initial.nativePlainText) {
            "no field took the text: nothing of it arrived in the focused text field, its first key typed twice " +
                "(the keyboard had not connected to the field yet, after focus moved to it; or the field " +
                "rejects every character typed, e.g. a full max-length field)"
        }
        if (blankNativeField) removeDuplicatePrefix(before.first, text)
        return true
    }

    private fun fieldState(field: Field? = focused()): Pair<Any, String>? {
        if (field == null) return null
        if (!field.editable) return null
        return field.identity to (if (field.showingHint) "" else field.text?.toString().orEmpty())
    }

    private fun removeDuplicatePrefix(identity: Any, text: String) {
        val field = focused() ?: return
        if (!field.editable || !field.nativePlainText || field.identity != identity || field.showingHint) return
        val observed = field.text?.toString() ?: return
        if (field.selectionStart != observed.length || field.selectionEnd != observed.length ||
            observed.length <= text.length || !observed.endsWith(text)) return
        val extra = observed.substring(0, observed.length - text.length)
        // Correct only a duplicated prefix in the same initially empty native
        // editor. Preserve masks, selection replacements and app formatting.
        if (!text.startsWith(extra) || !field.setText(text)) return
        field.select(text.length, text.length)
    }

    /**
     * Keys sent in chunks, each key stamped as it is sent and each chunk's last event waited
     * for. The input dispatcher drops key events older than 10 s, so stamping a long text all
     * at once loses its end on a slow device.
     */
    private fun injectKeys(presses: List<List<Key>>) {
        var next = 0
        while (next < presses.size) {
            val end = minOf(next + KEY_CHUNK, presses.size)
            for (k in next until end) {
                val down = clock.uptimeMillis()
                presses[k].forEachIndexed { j, e ->
                    injector.key(e.copy(downAt = down, at = clock.uptimeMillis()), k == end - 1 && j == presses[k].lastIndex)
                }
            }
            next = end
        }
    }

    private fun insertText(text: String): Boolean {
        val node = focused() ?: return false
        if (!node.editable) return false
        val current = if (node.showingHint) "" else node.text?.toString() ?: ""
        var start = node.selectionStart
        var end = node.selectionEnd
        if (start !in 0..current.length || end !in 0..current.length) { start = current.length; end = current.length }
        if (start > end) start = end.also { end = start }
        if (!node.setText(current.substring(0, start) + text + current.substring(end))) return false
        node.select(start + text.length, start + text.length)
        return true
    }

    /**
     * Deletes [count] characters from the end of the focused field. The cursor goes to the end
     * first: a tap on a field puts it where the finger landed, often mid-text, and deleting
     * before it would leave the end behind (typing then appends to what is left). Real DEL
     * keys follow, so the app sees key presses. The last DEL is waited for.
     */
    fun erase(count: Int) {
        cursorToEnd()
        val del = listOf(Key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DEL), Key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DEL))
        injectKeys(List(count.coerceIn(0, 10_000)) { del })
    }

    /** Moves the focused field's cursor to the end of its text (selection action, else the End key). */
    private fun cursorToEnd() {
        val node = focused()
        if (node != null && node.editable) {
            val len = if (node.showingHint) 0 else node.text?.length ?: 0
            if (node.selectionStart == len && node.selectionEnd == len) return
            if (node.select(len, len)) return
        }
        key(KeyEvent.KEYCODE_MOVE_END)
    }

    /** Clears the focused field in one action when possible. */
    fun clearFocused(): Boolean {
        val node = focused() ?: return false
        if (!node.editable) return false
        return node.setText("")
    }

    companion object {
        const val FRAME_MS = 16L
        /** A double tap: each contact's hold, and the time from the first UP to the second DOWN. */
        const val DOUBLE_TAP_CONTACT_MS = 100L
        const val DOUBLE_TAP_GAP_MS = 80L
        /** A no-fling drag: how long the finger seems to take, then rest before it ends. */
        const val DRAG_MS = 50L
        const val DRAG_REST_MS = 60L
        const val FRAME_STEP_MS = 8L
        const val KEY_CHUNK = 16
        /** How long checked typing waits for a field to take input focus (issue #97). */
        const val FOCUS_WAIT_MS = 1000
    }
}
