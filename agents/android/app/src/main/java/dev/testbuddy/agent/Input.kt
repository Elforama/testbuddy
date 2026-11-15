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
    /**
     * The focused editor's keyboard settings (EditorSettings.read), null when unknown; given
     * the focused field's app, which picks its section where a dump has several.
     */
    private val editorSettings: (pkg: String?) -> EditorSettings? = { null },
    private val focused: () -> Field?,
) {
    val path: String get() = injector.path

    /** The latest event time injected: a gesture's back-dated DOWN must not go before it. */
    private var lastEventAt = 0L

    /** False when the platform reported the event not injected. */
    private fun motion(down: Long, action: Int, x: Float, y: Float, sync: Boolean, at: Long = clock.uptimeMillis()): Boolean {
        lastEventAt = maxOf(lastEventAt, at)
        return injector.touch(Touch(action, down, at, x, y), sync)
    }

    /**
     * A tap. Its result: null when the platform took both events; "down" when it refused the
     * DOWN (nothing else is sent: the app saw nothing); "up" when it reported the UP not
     * delivered (issue #204). The DOWN may have reached the app then, so the gesture is
     * cancelled (no touch left down) and the tap may or may not have taken effect.
     */
    fun tap(x: Float, y: Float): String? {
        val down = clock.uptimeMillis()
        if (!motion(down, MotionEvent.ACTION_DOWN, x, y, false, down)) return "down"
        if (motion(down, MotionEvent.ACTION_UP, x, y, true)) return null
        motion(down, MotionEvent.ACTION_CANCEL, x, y, false)
        return "up"
    }

    /**
     * Two contacts whose timing is in their events' times: DOWN, UP [DOUBLE_TAP_CONTACT_MS]
     * later, the second DOWN [DOUBLE_TAP_GAP_MS] after that UP (inside the 300 ms double-tap
     * timeout, past the 40 ms minimum), its UP a contact later. Each contact's DOWN and UP go
     * in together, stamped in the past, and in real time one sleep of [DOUBLE_TAP_PAUSE_MS]
     * separates the taps.
     *
     * Apps time a double tap two ways. By the events' times: a GestureDetector's gap and the
     * Flutter bench's reaction row (75 ms contacts, UPs within 350 ms). By when the events
     * arrive: a click counter (the bench's doubletap_box: the second click within 300 ms of the
     * first), a GestureDetector's 300 ms timer, and Flutter's onDoubleTap, whose 40 ms minimum
     * is a timer started when the first DOWN is handled (a second DOWN handled before it fires
     * starts over). So the pause is long enough for that 40 ms timer on a busy UI thread and
     * short enough for the 300 ms windows when the sleep returns late.
     *
     * Holding the contacts in real time put two more sleeps between the taps, and on a loaded
     * emulator each sleep returns late (a 100 ms one took 191 ms): the second click came
     * 315 ms after the first (issue #144). Only the last UP waits for the app: waiting on the
     * others put the app's handling time between the taps, and on a slow emulator that pushed
     * them out of the window too (issue #89).
     */
    fun doubleTap(x: Float, y: Float) {
        // Back-dated so that after the pause the second UP's stamp is not in the future.
        val back = 2 * DOUBLE_TAP_CONTACT_MS + DOUBLE_TAP_GAP_MS - DOUBLE_TAP_PAUSE_MS
        // The input verifier drops a gesture whose DOWN predates the last event: after another
        // gesture within [back] ms, wait until the back-dated DOWN comes after it.
        val wait = lastEventAt - (clock.uptimeMillis() - back) + 1
        if (wait > 0) clock.sleep(wait)
        val down1 = clock.uptimeMillis() - back
        val up1 = down1 + DOUBLE_TAP_CONTACT_MS
        motion(down1, MotionEvent.ACTION_DOWN, x, y, false, down1)
        motion(down1, MotionEvent.ACTION_UP, x, y, false, up1)
        clock.sleep(DOUBLE_TAP_PAUSE_MS)
        // Stamped on the schedule however late the sleep returned: 100 / 80 / 100 ms exactly.
        // The pause is as long as [back] leaves, so both stamps are now or earlier.
        val down2 = up1 + DOUBLE_TAP_GAP_MS
        motion(down2, MotionEvent.ACTION_DOWN, x, y, false, down2)
        motion(down2, MotionEvent.ACTION_UP, x, y, true, down2 + DOUBLE_TAP_CONTACT_MS)
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
        if (len < 1f) { tap(x1, y1); return }
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
     *
     * Read afresh before every Enter that could become the action, never kept per editor
     * (issue #99 review): a Compose field can restart input with other settings on the same
     * node, which shows nothing of it, and right after a focus change several reads in a row
     * can still show the editor before. A kept answer would stay wrong for the field's life.
     * The read is the proto dump where there is one (Android 12+), asked of the input method
     * service in the agent's own process, with no shell process (issue #99: EditorSettings.read).
     */
    private fun enterIsNewline(f: Field): Boolean {
        val pkg = f.packageName?.toString()
        val s = editorSettings(pkg) ?: return false
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
     * connection has moved to it, and a key sent then is dropped. With nothing focused yet and
     * [waitForFocus] (the host's CI mode), it first waits (on accessibility events, up to
     * [FOCUS_WAIT_MS]) for a field to take focus. A native text field reports its text exactly,
     * so when it shows no change at all after the whole text (the first key retried), typing
     * fails instead of reporting text that went nowhere. A field that rejects only some
     * characters still passes.
     */
    fun typeChecked(text: String, events: Events, waitForFocus: Boolean = false, ci: Boolean = false): Boolean? {
        lastTyping = null
        if (text.isEmpty()) return type(text)
        var initial = focused()
        // A CI allowance (the host's CI mode, "ci" on the request): focus that is still moving
        // after an Enter lands late on a slow emulator. In CI typing waits for it; elsewhere a
        // field not focused yet goes to the host's guard at once, as before.
        if (initial == null && waitForFocus && events.awaitCondition(FOCUS_WAIT_MS) { focused() != null }) initial = focused()
        // No input yet: let the runner use its full-screen guard for custom widgets
        // whose editable accessibility node is absent from Android's focus API.
        if (initial == null) return null
        val before = fieldState(initial) ?: return null
        val blankNativeField = initial.nativePlainText && before.second.isEmpty() &&
            initial.selectionStart == 0 && initial.selectionEnd == 0
        val typing = Typing(text, before, initial, if (ci) ACK_CI_MS else ACK_MS)
        val end = text.offsetByCodePoints(0, 1)
        val first = text.substring(0, end)
        var took = false
        var seen: Pair<Any, String>? = before
        for (attempt in 0..1) {
            if (!type(first)) return false
            if (events.awaitCondition(1000) { fieldState().also { seen = it } != before }) { took = true; break }
        }
        // Not taken after a second try: the field rejects it (a filter), or it went before
        // the keyboard was connected; either way it is not looked for at the end.
        if (took) typing.acked[0] = true else typing.rejected[0] = true
        val rest = text.substring(end)
        // Issue #279: a field that does not show its first key as typed rewrites what is typed
        // (upper-casing, a mask, a formatter): its app replaces each new value, and the field's
        // input restarts. A key sent before that is through edits the value from before the key
        // ahead of it, and overwrites it. Such a field gets one key at a time, each once the
        // field shows the one before. A field that showed the key as typed keeps the chunks.
        // Judged on the read that saw the first key arrive: no read more for a field that echoes.
        val sameField = took && initial.nativePlainText && seen?.first == before.first
        val rewrites = sameField && seen?.second != typing.literal(first)
        if (rewrites) {
            typing.mode = "acknowledged"
            if (!typeAcknowledged(typing, 1, events, stopOnFocusMove = false)) return false
        } else {
            // Issue #297 review: a first key with no case (a digit, a space, a sign) echoed as
            // typed says nothing of what the field does to letters ("42 quick brown foxes" into an
            // upper-casing field). The keys go one at a time until a letter with a case shows as
            // typed (chunks for the rest) or a key is rewritten (one at a time for the rest).
            val chunkFrom = if (sameField && !typing.cased(0)) probe(typing, events) ?: return false else 1
            if (chunkFrom < typing.points.size && !type(text.substring(typing.offset(chunkFrom)))) return false
        }
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
        if (initial.nativePlainText) verifyTyped(typing, events)
        lastTyping = typing.mode
        return true
    }

    /**
     * How the last checked typing went (issue #279), for the reply: null when the keys went in
     * chunks and the field showed them all; "acknowledged" when they went one at a time, each
     * once the field showed the one before (a field that rewrites what is typed); "repaired"
     * when keys the field lost were typed again.
     */
    var lastTyping: String? = null
        private set

    /**
     * One checked typing's expectation (issue #279): the field's text before it, with the
     * selection replaced by [text]. The app may rewrite that (case, filters): the comparison
     * ignores case, and a key that the field showed no change for when it was sent alone (or the
     * first key, after its second try) counts as rejected by the app and is not looked for.
     */
    private class Typing(val text: String, before: Pair<Any, String>, field: Field, val ackMs: Int) {
        val identity = before.first
        val prefix: String
        val suffix: String
        /** The text's code points, each as a string. */
        val points: List<String> = buildList {
            var i = 0
            while (i < text.length) { val j = text.offsetByCodePoints(i, 1); add(text.substring(i, j)); i = j }
        }
        val rejected = BooleanArray(points.size)
        /** Keys the field was seen to take: sent alone, and the field changed (the first key, keys typed one at a time). */
        val acked = BooleanArray(points.size)
        var mode: String? = null
        /**
         * A key typed again in a repair changed the field to something other than its text with
         * that key added (issue #297 review): the repair stopped there.
         */
        var jumped: String? = null
        /** The focus left the field while its keys went in one at a time: no verdict, no repair. */
        var focusMoved = false

        init {
            val current = before.second
            var start = if (field.showingHint) 0 else field.selectionStart
            var end = if (field.showingHint) 0 else field.selectionEnd
            if (start !in 0..current.length || end !in 0..current.length) { start = current.length; end = current.length }
            if (start > end) start = end.also { end = start }
            prefix = current.substring(0, start)
            suffix = current.substring(end)
        }

        fun literal(typed: String) = prefix + typed + suffix

        /** Where code point [i] starts in [text]. */
        fun offset(i: Int): Int = points.take(i).sumOf { it.length }

        /** Whether code point [i] is a letter with a case: the key that shows what a field does to case. */
        fun cased(i: Int): Boolean {
            val cp = points[i].codePointAt(0)
            return Character.isLetter(cp) && (Character.toUpperCase(cp) != cp || Character.toLowerCase(cp) != cp)
        }

        /** The expected text up to code point [i] (included), as typed. */
        fun expectedUpTo(i: Int): String = (0..i).filter { !rejected[it] }.joinToString("") { points[it] }

        /** The code points (their indexes in [points]) the field should show. */
        fun expected(): List<Int> = points.indices.filter { !rejected[it] }

        /** The part of [value] between the text that was before and after the selection; null when that text changed. */
        fun typedPart(value: String): String? {
            if (value.length < prefix.length + suffix.length) return null
            if (!value.startsWith(prefix, ignoreCase = true) || !value.endsWith(suffix, ignoreCase = true)) return null
            return value.substring(prefix.length, value.length - suffix.length)
        }

        /**
         * How much of the expected text [typed] holds from its start: the number of expected
         * code points matched, and the length of [typed] they take.
         */
        fun commonStart(typed: String): Pair<Int, Int> {
            val want = expected()
            var k = 0
            var at = 0
            while (k < want.size) {
                val p = points[want[k]]
                if (!typed.regionMatches(at, p, 0, p.length, ignoreCase = true)) break
                at += p.length
                k++
            }
            return k to at
        }

        /** Whether [typed] is the expected text with some code points missing (a strict subsequence). */
        fun short(typed: String): Boolean {
            val want = expected()
            var at = 0
            for (i in want) {
                if (at == typed.length) break
                val p = points[i]
                if (typed.regionMatches(at, p, 0, p.length, ignoreCase = true)) at += p.length
            }
            return at == typed.length && !matches(typed)
        }

        /** Whether [typed] ends with the last key expected: the keys typed have all come through. */
        fun lastArrived(typed: String): Boolean {
            val last = expected().lastOrNull() ?: return true
            return typed.endsWith(points[last], ignoreCase = true)
        }

        /** The expected code points [typed] lacks (their indexes in [points]), matched from the start. */
        fun missing(typed: String): List<Int> {
            val out = ArrayList<Int>()
            var at = 0
            for (i in expected()) {
                val p = points[i]
                if (at < typed.length && typed.regionMatches(at, p, 0, p.length, ignoreCase = true)) at += p.length
                else out += i
            }
            return out
        }

        /**
         * Whether [typed] shows keys lost: the expected text with keys missing, its last key
         * there, and a missing key the field was seen to take. A key is lost to the key after it
         * (which edits the value from before it), so the last key is never lost that way: a
         * text whose end is missing was cut or cleared by the app (a max length, a code field
         * that clears itself). Only keys typed one at a time are judged (issue #297 review): a
         * key the field showed, then lost. A key the field showed no change for was rejected
         * by the app (a filter) and is not looked for.
         */
        fun lostKeys(typed: String): Boolean {
            if (!short(typed) || !lastArrived(typed)) return false
            return missing(typed).any { acked[it] }
        }

        fun matches(typed: String): Boolean = expected().joinToString("") { points[it] }.equals(typed, ignoreCase = true)
    }

    /**
     * Types [Typing.points] from [from] one at a time (issue #279): each key once the field
     * shows the change the key before made, waited for on accessibility events and checked
     * every frame (Compose reports its changes at most every 100 ms), up to [Typing.ackMs]. A
     * key the field showed no change for is taken as rejected by the app (a filter). No fixed
     * sleeps: a field that takes a key in a frame gets the next one a frame later.
     *
     * Known limit: a key the field takes only after [Typing.ackMs] counts as rejected, so if the
     * next key then overwrites it, its loss is not looked for at the end and the step passes.
     * That takes a frame slower than 300 ms (1 s in CI mode); it never fails a step wrongly.
     *
     * Focus that leaves the field marks [Typing.focusMoved]. When typing the text, keys go on to
     * the field focused, as a keyboard's would; with [stopOnFocusMove] (a repair) no key more
     * is sent.
     */
    private fun typeAcknowledged(typing: Typing, from: Int, events: Events, stopOnFocusMove: Boolean): Boolean {
        for (i in from until typing.points.size) {
            val prev = fieldState()
            if (prev?.first != typing.identity) {
                typing.focusMoved = true
                if (stopOnFocusMove) return true
            }
            if (!type(typing.points[i])) return false
            val changed = awaitFieldChange(prev, typing.ackMs, events)
            typing.acked[i] = changed
            typing.rejected[i] = !changed
            // A repair (issue #297 review): each key must add itself to the text and nothing
            // else. A field that shows more (keys that were only late, now arriving) or anything
            // else stops the repair at once, and the step fails: a key typed twice at most, and
            // said so.
            if (stopOnFocusMove && changed) {
                val was = prev?.let { typing.typedPart(it.second) }
                val now = fieldState()?.takeIf { it.first == typing.identity }?.let { typing.typedPart(it.second) }
                if (was == null || now == null || !now.equals(was + typing.points[i], ignoreCase = true)) {
                    typing.jumped = now ?: ""
                    return true
                }
            }
        }
        return true
    }

    /**
     * Types the keys after a first key with no case that the field showed as typed (issue #297
     * review), one at a time, until one tells what the field does: a letter with a case shown as
     * typed (the field echoes keys: the index from which the rest goes in chunks), or a key the
     * field rewrote (the rest goes one at a time: [Typing.points]' size). The rest goes in chunks
     * too once it holds no letter with a case. Null when a key could not be typed.
     */
    private fun probe(typing: Typing, events: Events): Int? {
        for (i in 1 until typing.points.size) {
            if ((i until typing.points.size).none { typing.cased(it) }) return i
            val prev = fieldState()
            if (prev?.first != typing.identity) return i // focus moved: keys follow it, in chunks
            if (!type(typing.points[i])) return null
            val changed = awaitFieldChange(prev, typing.ackMs, events)
            typing.acked[i] = changed
            typing.rejected[i] = !changed
            if (!changed) continue // rejected: it shows nothing of the field
            val now = fieldState()
            if (now?.first != typing.identity) return i + 1
            if (now.second != typing.literal(typing.expectedUpTo(i))) {
                typing.mode = "acknowledged"
                return if (typeAcknowledged(typing, i + 1, events, stopOnFocusMove = false)) typing.points.size else null
            }
            if (typing.cased(i)) return i + 1
        }
        return typing.points.size
    }

    /** Waits until the focused field's state is not [prev]: on events, and read every frame. */
    private fun awaitFieldChange(prev: Pair<Any, String>?, capMs: Int, events: Events): Boolean =
        awaitField(capMs, events) { fieldState() != prev }

    private fun awaitField(capMs: Int, events: Events, condition: () -> Boolean): Boolean {
        val deadline = clock.uptimeMillis() + capMs
        while (true) {
            val left = deadline - clock.uptimeMillis()
            if (left <= 0) return condition()
            if (events.awaitCondition(minOf(left, FRAME_MS).toInt(), condition)) return true
        }
    }

    /**
     * Issue #279: the postcondition of checked typing into a native plain-text field. The field
     * must hold what it held before with the selection replaced by the text (ignoring case, and
     * the keys the app rejected). When it holds that text with keys lost ([Typing.lostKeys]),
     * the keys from the first missing one are deleted (one DEL at a time, read after each: a DEL
     * takes a whole emoji or accented letter) and typed again, one at a time, once, in the same
     * field. Still missing keys, or the focus gone from the field during that repair, the step
     * fails, naming what the field holds. A field that shows something else (a mask's or
     * formatter's characters, autocorrection, a text cut or cleared by the app, keys of a kind
     * it never shows, focus that moved, the text around the selection rewritten) gets no
     * verdict, as before.
     *
     * Only keys typed one at a time are judged (issue #297 review): a field that echoes its keys
     * gets them in chunks and costs nothing here. For keys typed one at a time, a text that
     * ends short of its last key is read until it has stayed the same for [STABLE_MS], two
     * frames; a loss, for the whole cap.
     */
    private fun verifyTyped(typing: Typing, events: Events) {
        // Issue #297 review: only keys typed one at a time are judged. Keys lost to the restart of
        // a field that rewrites its value are seen there (each key shown, then overwritten). A
        // field that echoes keys gets them in chunks and no verdict: a field only behind (a
        // Flutter field, keys shown several frames late, in bursts) looks the same as one that
        // lost keys, and deleting and typing again on it typed keys twice ("gammamma").
        if (typing.mode != "acknowledged" || typing.focusMoved) return
        fun typedNow(): String? {
            val (id, value) = fieldState() ?: return null
            return if (id == typing.identity) typing.typedPart(value) else null
        }
        // The last keys can still be on their way to the field (a Compose field shows a value
        // a frame later): wait for the last key, or for anything that is not part of the text,
        // or for the text to stop changing: the same for [STABLE_MS] (two frames), as a field
        // at its max length or one that cleared itself shows at once. The cap bounds a field
        // that keeps changing. That last exit is the first read's only ([stableExit]): it
        // passes a field with no verdict. After a repair, a short text fails the step, so the
        // text is waited for up to the cap: a slow field is not failed two frames after it.
        fun settled(stableExit: Boolean): String? {
            var t: String? = null
            var since = 0L
            awaitField(typing.ackMs, events) {
                val now = typedNow()
                val at = clock.uptimeMillis()
                if (now != t || since == 0L) since = at
                t = now
                now == null || !typing.short(now) || typing.lastArrived(now) || stableExit && at - since >= STABLE_MS
            }
            return t
        }
        // Issue #297: a loss is acted on only when it is final. A field that is only behind (a
        // Flutter field shows keys several frames late) can show a subsequence ending in the
        // last key, "ga" of "gamma", for longer than two frames; DELs and keys sent on that
        // went in on top of the real text ("gammamma"). So a loss seen is waited on until the
        // text has stayed the same for the whole cap (300 ms; 1 s in CI mode) and still shows
        // the loss: the verdict. A text that changes meanwhile is judged again, up to
        // [VERDICT_ROUNDS] times; one that keeps changing gets no verdict. The no-verdict
        // outcomes (the text whole, filters, a text cut short) keep the two-frame exit above.
        fun verdict(): String? {
            var t = settled(stableExit = true) ?: return null
            repeat(VERDICT_ROUNDS) {
                if (!typing.lostKeys(t)) return null
                val seen = t
                if (!awaitField(typing.ackMs, events) { typedNow() != seen }) return seen
                t = settled(stableExit = true) ?: return null
            }
            return null
        }
        // A repair starts only on a final verdict, and is called off (before its first DEL) when
        // the text is no longer the text judged: then it is judged again, once.
        var typed: String = verdict() ?: return
        var placed = false
        for (attempt in 0..1) {
            val f = focused() ?: return
            val cursor = typing.prefix.length + typed.length
            placed = f.selectionStart == cursor && f.selectionEnd == cursor || f.select(cursor, cursor)
            if (!placed || typedNow() == typed) break
            if (attempt == 1) return // still changing: no verdict, nothing deleted
            typed = verdict() ?: return
        }
        val at = typing.commonStart(typed).second
        var now: String? = typed
        var lostFocus = false
        var moved = false
        var stalled = false
        var retyped = false
        if (placed) {
            typing.mode = "repaired"
            // One DEL at a time, read after each, until only the part that is right is left: a
            // DEL deletes a grapheme, which can be several code points. At most one per char.
            var budget = typed.length - at
            while (budget-- > 0) {
                val t = now ?: break
                if (t.length <= at) break
                when (repairDel(typing.identity, typing.ackMs, events) { typing.typedPart(it) == t }) {
                    RepairDel.FOCUS_LEFT -> lostFocus = true
                    RepairDel.MOVED -> moved = true
                    RepairDel.STALLED -> stalled = true
                    RepairDel.SHOWN -> {}
                }
                if (lostFocus || moved || stalled) break
                now = typedNow()
                if (now == null && fieldState()?.first != typing.identity) { lostFocus = true; break }
            }
            if (moved || stalled) now = settled(stableExit = false)
            val t = now
            if (!lostFocus && !moved && !stalled && t != null) {
                val (matched, right) = typing.commonStart(t)
                // Typed again only onto text that is right (a DEL that took nothing leaves more).
                if (right == t.length) {
                    val want = typing.expected()
                    if (matched < want.size) {
                        val from = want[matched]
                        for (i in from until typing.points.size) typing.rejected[i] = false
                        typeAcknowledged(typing, from, events, stopOnFocusMove = true)
                        lostFocus = typing.focusMoved
                    }
                    if (!lostFocus) now = settled(stableExit = false)
                    if (typing.jumped != null) now = typing.jumped
                    retyped = true
                }
            }
        }
        val expected = typing.literal(typing.text)
        check(!lostFocus) {
            "typing lost keys, and the focus left the field while they were typed again: it held " +
                "\"${typing.prefix + typed + typing.suffix}\", expected \"$expected\""
        }
        val end = now
        check(end != null && !typing.short(end) && typing.jumped == null && !stalled) {
            val holds = end?.let { typing.prefix + it + typing.suffix }
                ?: focused()?.let { if (it.showingHint) "" else it.text?.toString() }.orEmpty()
            "typing lost keys: the field holds \"$holds\", expected \"$expected\"" +
                when {
                    typing.jumped != null -> " (a key typed again to repair it made the field show more than that key: the repair stopped there)"
                    stalled -> " (a DEL to repair it did not show within the wait: the repair stopped there)"
                    moved -> " (the field's text changed while the keys after the first missing one were deleted, so nothing was typed again)"
                    retyped -> " (the missing keys were typed again, one at a time, and still did not arrive)"
                    placed -> " (deleting back to the first missing key did not leave the text before it, so nothing was typed again)"
                    else -> " (the cursor could not be put after the typed text to type the missing keys again)"
                }
        }
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

    /** What became of one repair DEL ([repairDel]). */
    private enum class RepairDel { SHOWN, FOCUS_LEFT, MOVED, STALLED }

    /**
     * One DEL of a repair, shared by checked typing's (issues #279, #297) and erasing's (issue
     * #289). It goes only into the field [identity] (else FOCUS_LEFT), and only when the field's
     * text, read afresh right before it, is still the text the repair judged ([seen]; else
     * MOVED): a text that moved on was not seen, and a DEL on a read that was only lagging takes
     * what is right ("gammamma", issue #297). The field must show the DEL within [ackMs] (else
     * STALLED): one it did not show may still be on its way, and the next could delete too much.
     */
    private fun repairDel(identity: Any, ackMs: Int, events: Events, seen: (String) -> Boolean): RepairDel {
        val prev = fieldState()
        if (prev?.first != identity) return RepairDel.FOCUS_LEFT
        if (!seen(prev.second)) return RepairDel.MOVED
        injectKeys(listOf(DEL))
        return if (awaitFieldChange(prev, ackMs, events)) RepairDel.SHOWN else RepairDel.STALLED
    }

    /**
     * Erasing's repair of text left over at the end (issue #289): one [repairDel] at a time,
     * from [start] (the still text judged), at most [budget], until the field is erased as
     * asked ([Erasing.done]) or holds no more than [Erasing.expected]. A text that moved on, or
     * a DEL not shown, ends it. Returns whether the focus left, and whether a DEL was shown.
     */
    private fun eraseBack(e: Erasing, start: String, budget: Int, events: Events, read: () -> String?): Pair<Boolean, Boolean> {
        var seen = start
        var left = budget
        var shown = false
        while (left-- > 0) {
            if (e.done(seen) || seen.length <= e.expected.length) break
            when (repairDel(e.identity, e.ackMs, events) { it == seen }) {
                RepairDel.FOCUS_LEFT -> return true to shown
                RepairDel.MOVED, RepairDel.STALLED -> break
                RepairDel.SHOWN -> { shown = true; seen = read() ?: return (fieldState()?.first != e.identity) to true }
            }
        }
        return false to shown
    }

    /**
     * eraseText (issue #289): deletes [count] characters from the end of the focused field's
     * text, as Maestro's eraseText does (that many DEL presses; erasing "all" is the default 50).
     * Its postcondition: the field holds its old text less its last [count] characters (all of
     * it when it holds no more), and a step that cannot establish that fails.
     *
     * - **The caret goes to the end first, always.** A tap puts it where the finger landed,
     *   often mid-text, and a DEL deletes before it. A field's reported selection is not trusted
     *   to skip that: a Compose field reported its caret at the end while it was mid-text, and
     *   the DELs took what was before the caret and left the tail ("S FAST").
     * - **Real DEL keys** then, so the app sees key presses (a code box whose DEL in an empty box
     *   moves the focus back gets the rest), and the last is waited for.
     * - **Read back** (a native plain-text field: one that reports its text exactly, as typing's
     *   check): on events and every frame, until it is right, or it has stayed the same for a
     *   whole per-key cap, up to [SETTLE_CAPS] caps. A field still catching up on the DELs (a
     *   lagging read, issue #297) is never judged or repaired, and one still changing then gets
     *   no verdict.
     * - **Only what DELs leave when they went wrong is judged** ([Erasing.judged]): text left
     *   over at the end, or text taken from before a caret that was not at the end. Anything
     *   else (a mask, a formatter, a prefix the app puts back) is the app's own: no verdict.
     * - **Repaired once** when it is judged, not right and stable, on a fresh read: erasing all sets the
     *   text empty (the platform's own edit); text left over at the end is deleted one DEL at a
     *   time ([eraseBack]: read afresh before each DEL, stopped by a DEL that shows no change);
     *   text taken from before the end (a caret that could not be moved) is set to what the
     *   erase leaves.
     * - Still not right: the step fails, naming what the field holds and what was expected. The
     *   focus gone from the field after the DELs (code boxes) gives no verdict, as for typing.
     *
     * Its cost for a field that shows the DELs at once: one selection action and one read.
     */
    fun erase(count: Int, events: Events, ci: Boolean = false) {
        lastErase = null
        val n = count.coerceIn(0, 10_000)
        if (n == 0) return
        val f = focused()
        val before = fieldState(f)
        val erasing = if (f != null && f.nativePlainText && before != null) Erasing(before, n, if (ci) ACK_CI_MS else ACK_MS) else null
        cursorToEnd(f)
        injectKeys(List(n) { DEL })
        if (erasing != null) verifyErased(erasing, events)
    }

    /**
     * How the last erase went (issue #289), for the reply: null when the DELs left the field as
     * expected (or it got no verdict); "repaired" when it was put right after them.
     */
    var lastErase: String? = null
        private set

    /**
     * One erase's expectation (issue #289): [old] less its last [count] characters. A DEL
     * deletes a grapheme (an emoji with its skin tone, a letter with its accent), so what is
     * left is cut at a grapheme ([graphemeCut]); a field that deletes code points instead
     * leaves a longer prefix, which counts as right too.
     */
    private class Erasing(before: Pair<Any, String>, val count: Int, val ackMs: Int) {
        val identity = before.first
        val old = before.second
        val expected: String = old.substring(0, graphemeCut(old, count))
        private val longest = old.substring(0, codePointCut(old, count))

        /** Whether the field holding [value] is erased as asked. */
        fun done(value: String) = value.length in expected.length..longest.length && old.startsWith(value)

        /**
         * Whether [value] is what DELs leave when they went wrong, the only text erasing judges
         * and repairs (issue #289 review, as typing judges only lost keys): text left over at
         * the end (a prefix of [old], longer than [expected]: DELs that did not all arrive), or
         * [old] with a run taken out before its end (the DELs went in at a caret that was not
         * at the end). Anything else is the app's own (a mask, a formatter, a prefix or a "0" it
         * puts back): no verdict.
         */
        fun judged(value: String): Boolean {
            if (value.length > expected.length && old.startsWith(value)) return true
            val k = old.length - value.length
            if (k <= 0) return false
            var p = 0
            while (p < value.length && old[p] == value[p]) p++
            var s = 0
            while (s < value.length && old[old.length - 1 - s] == value[value.length - 1 - s]) s++
            // A caret c (k <= c < old.length): old's first c - k chars, then old from c.
            return maxOf(k, old.length - s) <= minOf(p + k, old.length - 1)
        }
    }

    /** Issue #289: the postcondition of erasing, and its one repair (see [erase]). */
    private fun verifyErased(e: Erasing, events: Events) {
        fun now(): String? {
            val (id, value) = fieldState() ?: return null
            return if (id == e.identity) value else null
        }
        // What the field holds once it is right, gone from the field, or the same for the whole
        // cap (issue #297: never judged, nor repaired, on a read that may only be lagging). A
        // field still changing after [SETTLE_CAPS] caps is returned as it is then, with [stable]
        // false: it is not repaired.
        var stable = false
        fun settled(): String? {
            var t: String? = null
            var since = clock.uptimeMillis()
            stable = false
            awaitField(SETTLE_CAPS * e.ackMs, events) {
                val v = now()
                val at = clock.uptimeMillis()
                if (v != t) since = at
                t = v
                stable = at - since >= e.ackMs
                v == null || e.done(v) || stable
            }
            return t
        }
        // Focus gone from the field (code boxes moving back): no verdict. A field still changing
        // after [SETTLE_CAPS] caps: no verdict either, as typing's after its rounds (a slow
        // device is not failed). Text that is not what DELs leave when they went wrong is the
        // app's own (a mask, a formatter): no verdict, nothing set or deleted.
        val left = settled() ?: return
        if (e.done(left) || !stable || !e.judged(left)) return
        val f = focused() ?: return
        if (fieldState(f)?.first != e.identity) return
        // Read afresh right before acting: a text that moved on since it was judged is not
        // repaired (issue #297).
        if (now() != left) return
        lastErase = "repaired"
        var lostFocus = false
        val took: Boolean
        if (e.expected.isNotEmpty() && left.startsWith(e.expected)) {
            // Text left over at the end: from the end, one DEL at a time.
            cursorToEnd(f)
            val (lost, shown) = eraseBack(e, left, minOf(left.length - e.expected.length, e.count), events, ::now)
            lostFocus = lost
            took = shown
        } else {
            // All to erase, or text taken from before the end: the text the erase leaves.
            took = f.setText(e.expected)
        }
        check(!lostFocus) {
            "erasing: the focus left the field while what was left was deleted: it held \"$left\", expected \"${e.expected}\""
        }
        val end = settled()
        // The field went back to the very text it held after a repair it took: the app writes
        // that text itself (a prefix it puts back), so it is its own: no verdict. Still changing
        // after the repair: no verdict either.
        if (end == null || e.done(end) || !stable || took && end == left) return
        check(false) {
            "erasing: the field holds \"$end\", expected \"${e.expected}\"" + when {
                !took && e.expected.isNotEmpty() && left.startsWith(e.expected) ->
                    " (its caret was put at the end and the DELs sent; the DEL to delete what was left showed no change)"
                !took -> " (its caret was put at the end and the DELs sent; setting the text it should hold was refused)"
                else -> " (its caret was put at the end, the DELs sent, and the field put right once)"
            }
        }
    }

    /**
     * Moves [node]'s caret to the end of its text: the selection action, else the End key. The
     * action goes even when the field reports its caret at the end (issue #289): a Compose field
     * reported that while its caret was mid-text.
     */
    private fun cursorToEnd(node: Field? = focused()) {
        if (node != null && node.editable) {
            val len = if (node.showingHint) 0 else node.text?.length ?: 0
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
        /**
         * A double tap's real time between sending the first contact and the second: past
         * Flutter's 40 ms onDoubleTap timer with 80 ms to spare for a busy UI thread, and
         * leaving 180 ms of the 300 ms click window for the sleep to return late (on a loaded
         * emulator an 80 ms sleep took up to 215 ms).
         */
        const val DOUBLE_TAP_PAUSE_MS = 120L
        /** A no-fling drag: how long the finger seems to take, then rest before it ends. */
        const val DRAG_MS = 50L
        const val DRAG_REST_MS = 60L
        const val FRAME_STEP_MS = 8L
        const val KEY_CHUNK = 16
        /** How long checked typing waits for a field to take input focus in CI mode (issue #97). */
        const val FOCUS_WAIT_MS = 1000
        /**
         * How long a key typed one at a time waits for the field to show it (issue #279): a
         * field takes a key in a frame or two. Past this the key counts as rejected by the app,
         * and the next is sent; in CI mode a slow emulator gets longer.
         */
        const val ACK_MS = 300
        const val ACK_CI_MS = 1000
        /**
         * How long a typed field's text that ends short of its last key must stay the same to be
         * taken as final (issue #279): two frames, so a Compose field a frame behind is waited for.
         */
        const val STABLE_MS = 2 * FRAME_MS
        /**
         * How many times a loss seen at the end of typing is waited on for a still text before
         * it is given no verdict (issue #297): each round is up to the cap.
         */
        const val VERDICT_ROUNDS = 3

        /**
         * How many per-key caps erasing waits at most for a field's text to be right or to stay
         * the same for a whole cap before it is judged (issue #289; #297: no repair on a lagging read).
         */
        const val SETTLE_CAPS = 3

        /** A DEL key press. */
        private val DEL = listOf(Key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DEL), Key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DEL))

        /** Where [s] is cut when [count] code points are taken from its end. */
        fun codePointCut(s: String, count: Int): Int {
            var at = s.length
            var n = count
            while (n-- > 0 && at > 0) at = s.offsetByCodePoints(at, -1)
            return at
        }

        /**
         * Where [s] is cut when [count] graphemes, what a DEL deletes, are taken from its end
         * (issue #289). A code point joins the one before it when it is a combining mark, a
         * variation selector, a skin tone, a zero-width joiner or what follows one, a tag, or
         * the second of a pair of regional indicators (a flag).
         */
        fun graphemeCut(s: String, count: Int): Int {
            val starts = ArrayList<Int>()
            var i = 0
            var prev = -1
            var flags = 0
            while (i < s.length) {
                val cp = s.codePointAt(i)
                val flag = cp in 0x1F1E6..0x1F1FF
                val joins = prev != -1 && (
                    Character.getType(cp).let {
                        it == Character.NON_SPACING_MARK.toInt() || it == Character.ENCLOSING_MARK.toInt() ||
                            it == Character.COMBINING_SPACING_MARK.toInt()
                    } || cp in 0xFE00..0xFE0F || cp in 0x1F3FB..0x1F3FF || cp == 0x200D || prev == 0x200D ||
                        cp in 0xE0020..0xE007F || flag && flags % 2 == 1
                    )
                flags = if (flag) flags + 1 else 0
                if (!joins) starts += i
                prev = cp
                i += Character.charCount(cp)
            }
            return if (count >= starts.size) 0 else starts[starts.size - count]
        }
    }
}
