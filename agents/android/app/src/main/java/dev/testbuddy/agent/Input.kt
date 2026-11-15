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
        val rewrites = took && initial.nativePlainText && seen?.let { (id, now) ->
            id == before.first && now != typing.literal(first)
        } == true
        if (rewrites) {
            typing.mode = "acknowledged"
            if (!typeAcknowledged(typing, 1, events, stopOnFocusMove = false)) return false
        } else if (!type(rest)) return false
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
         * there, and a missing key the field is known to take. A key is lost to the key after
         * it (which edits the value from before it), so the last key is never lost that way: a
         * text whose end is missing was cut or cleared by the app (a max length, a code field
         * that clears itself). A missing key the field was seen to take when sent alone is
         * lost; one sent in a chunk is lost only when the field shows a character of its kind
         * (a letter, a digit, a space; any other character itself) elsewhere in the text. A
         * field that never shows that kind filters it out (no spaces, digits only, a phone
         * field dropping "+()-"): the app's own, no verdict, and nothing deleted to find out.
         */
        fun lostKeys(typed: String): Boolean {
            if (!short(typed) || !lastArrived(typed)) return false
            val shown = HashSet<String>()
            var i = 0
            while (i < typed.length) { val cp = typed.codePointAt(i); shown += kind(cp); i += Character.charCount(cp) }
            return missing(typed).any { acked[it] || kind(points[it].codePointAt(0)) in shown }
        }

        private fun kind(cp: Int): String = when {
            Character.isLetter(cp) -> "letter"
            Character.isDigit(cp) -> "digit"
            Character.isWhitespace(cp) -> "space"
            else -> String(Character.toChars(Character.toLowerCase(cp)))
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
        }
        return true
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
     * Its cost for a field that echoes the keys: one read of the focused field when the text is
     * there at once (and one more for the rewrite check, made on the first key's read). A
     * Compose field shows its last keys a frame later: reads every frame until they show. A
     * text that ends short of its last key (a max length, a field that cleared itself) is read
     * until it has stayed the same for [STABLE_MS], two frames; a field still changing, for up
     * to the cap.
     */
    private fun verifyTyped(typing: Typing, events: Events) {
        if (typing.focusMoved) return
        fun typedNow(): String? {
            val (id, value) = fieldState() ?: return null
            return if (id == typing.identity) typing.typedPart(value) else null
        }
        // The last keys can still be on their way to the field (a Compose field shows a value
        // a frame later): wait for the last key, or for anything that is not part of the text,
        // or for the text to stop changing: the same for [STABLE_MS] (two frames), as a field
        // at its max length or one that cleared itself shows at once. The cap bounds a field
        // that keeps changing.
        fun settled(): String? {
            var t: String? = null
            var since = 0L
            awaitField(typing.ackMs, events) {
                val now = typedNow()
                val at = clock.uptimeMillis()
                if (now != t || since == 0L) since = at
                t = now
                now == null || !typing.short(now) || typing.lastArrived(now) || at - since >= STABLE_MS
            }
            return t
        }
        val typed = settled() ?: return
        if (!typing.lostKeys(typed)) return
        val f = focused() ?: return
        val at = typing.commonStart(typed).second
        val cursor = typing.prefix.length + typed.length
        val placed = f.selectionStart == cursor && f.selectionEnd == cursor || f.select(cursor, cursor)
        var now: String? = typed
        var lostFocus = false
        var retyped = false
        if (placed) {
            typing.mode = "repaired"
            val del = listOf(Key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DEL), Key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DEL))
            // One DEL at a time, read after each, until only the part that is right is left: a
            // DEL deletes a grapheme, which can be several code points. At most one per char.
            var budget = typed.length - at
            while (budget-- > 0) {
                val t = now ?: break
                if (t.length <= at) break
                val prev = fieldState()
                if (prev?.first != typing.identity) { lostFocus = true; break }
                injectKeys(listOf(del))
                awaitFieldChange(prev, typing.ackMs, events)
                now = typedNow()
                if (now == null && fieldState()?.first != typing.identity) { lostFocus = true; break }
            }
            val t = now
            if (!lostFocus && t != null) {
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
                    if (!lostFocus) now = settled()
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
        check(end != null && !typing.short(end)) {
            val holds = end?.let { typing.prefix + it + typing.suffix }
                ?: focused()?.let { if (it.showingHint) "" else it.text?.toString() }.orEmpty()
            "typing lost keys: the field holds \"$holds\", expected \"$expected\"" +
                when {
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
    }
}
