package dev.testbuddy.agent

import android.text.InputType
import android.view.KeyEvent
import android.view.MotionEvent

class Input(
    private val injector: Injector,
    private val clock: Clock,
    private val editorSettings: (pkg: String?) -> EditorSettings? = { null },
    private val focused: () -> Field?,
) {
    val path: String get() = injector.path

    private var lastEventAt = 0L

    private fun motion(down: Long, action: Int, x: Float, y: Float, sync: Boolean, at: Long = clock.uptimeMillis()): Boolean {
        lastEventAt = maxOf(lastEventAt, at)
        return injector.touch(Touch(action, down, at, x, y), sync)
    }

    fun tap(x: Float, y: Float): String? {
        val down = clock.uptimeMillis()
        if (!motion(down, MotionEvent.ACTION_DOWN, x, y, false, down)) return "down"
        if (motion(down, MotionEvent.ACTION_UP, x, y, true)) return null
        motion(down, MotionEvent.ACTION_CANCEL, x, y, false)
        return "up"
    }

    fun doubleTap(x: Float, y: Float) {
        val back = 2 * DOUBLE_TAP_CONTACT_MS + DOUBLE_TAP_GAP_MS - DOUBLE_TAP_PAUSE_MS
        val wait = lastEventAt - (clock.uptimeMillis() - back) + 1
        if (wait > 0) clock.sleep(wait)
        val down1 = clock.uptimeMillis() - back
        val up1 = down1 + DOUBLE_TAP_CONTACT_MS
        motion(down1, MotionEvent.ACTION_DOWN, x, y, false, down1)
        motion(down1, MotionEvent.ACTION_UP, x, y, false, up1)
        clock.sleep(DOUBLE_TAP_PAUSE_MS)
        val down2 = up1 + DOUBLE_TAP_GAP_MS
        motion(down2, MotionEvent.ACTION_DOWN, x, y, false, down2)
        motion(down2, MotionEvent.ACTION_UP, x, y, true, down2 + DOUBLE_TAP_CONTACT_MS)
    }

    fun press(x: Float, y: Float, holdMs: Long) {
        val down = clock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x, y, true, down)
        clock.sleep(holdMs)
        motion(down, MotionEvent.ACTION_UP, x, y, true)
    }

    fun swipe(x1: Float, y1: Float, x2: Float, y2: Float, durationMs: Long) {
        injector.prepareSwipe()
        val down = clock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x1, y1, false, down)
        val steps = maxOf(2, (durationMs / FRAME_MS).toInt())
        val start = clock.uptimeMillis()
        for (i in 1..steps) {
            val frac = i.toFloat() / steps
            val target = start + (durationMs * frac).toLong()
            val wait = target - clock.uptimeMillis()
            if (wait > 0) clock.sleep(wait)
            motion(down, MotionEvent.ACTION_MOVE, x1 + (x2 - x1) * frac, y1 + (y2 - y1) * frac, false)
        }
        motion(down, MotionEvent.ACTION_UP, x2, y2, true)
    }

    fun holdDrag(x1: Float, y1: Float, x2: Float, y2: Float, holdMs: Long, durationMs: Long) {
        injector.prepareSwipe()
        val down = clock.uptimeMillis()
        check(motion(down, MotionEvent.ACTION_DOWN, x1, y1, true, down)) { "drag: the platform refused the touch down, nothing was dragged" }
        if (holdMs > 0) clock.sleep(holdMs)
        val len = Math.hypot((x2 - x1).toDouble(), (y2 - y1).toDouble())
        val byDistance = Math.ceil(len / (HOLD_DRAG_STEP_DP * density())).toInt()
        val glide = maxOf(durationMs, HOLD_DRAG_MIN_MOVE_MS, byDistance * FRAME_MS)
        val steps = maxOf(1, byDistance, (glide / FRAME_MS).toInt())
        val start = clock.uptimeMillis()
        for (i in 1..steps) {
            val frac = i.toFloat() / steps
            val wait = start + (glide * frac).toLong() - clock.uptimeMillis()
            if (wait > 0) clock.sleep(wait)
            motion(down, MotionEvent.ACTION_MOVE, x1 + (x2 - x1) * frac, y1 + (y2 - y1) * frac, i == steps)
        }
        val past = if (len >= 1) minOf(HOLD_DRAG_SETTLE_DP * density(), len).toFloat() else 0f
        val ux = if (len >= 1) ((x2 - x1) / len).toFloat() else 0f
        val uy = if (len >= 1) ((y2 - y1) / len).toFloat() else 0f
        val dropAt = clock.uptimeMillis()
        for (k in 1..HOLD_DRAG_DROP_MS / FRAME_MS) {
            val wait = dropAt + k * FRAME_MS - clock.uptimeMillis()
            if (wait > 0) clock.sleep(wait)
            val f = when {
                k <= HOLD_DRAG_SETTLE_FRAMES -> k.toFloat() / HOLD_DRAG_SETTLE_FRAMES
                k <= 2 * HOLD_DRAG_SETTLE_FRAMES -> (2 * HOLD_DRAG_SETTLE_FRAMES - k).toFloat() / HOLD_DRAG_SETTLE_FRAMES
                else -> 0f
            }
            motion(down, MotionEvent.ACTION_MOVE, x2 + ux * past * f, y2 + uy * past * f, false)
        }
        val left = dropAt + HOLD_DRAG_DROP_MS - clock.uptimeMillis()
        if (left > 0) clock.sleep(left)
        motion(down, MotionEvent.ACTION_UP, x2, y2, true)
    }

    fun drag(x1: Float, y1: Float, x2: Float, y2: Float) {
        val dx = x2 - x1
        val dy = y2 - y1
        val len = Math.hypot(dx.toDouble(), dy.toDouble()).toFloat()
        if (len < 1f) { tap(x1, y1); return }
        val back = DRAG_MS + DRAG_REST_MS
        val gap = lastEventAt - (clock.uptimeMillis() - back) + 1
        if (gap > 0) clock.sleep(gap)
        val down = clock.uptimeMillis() - back
        val ux = dx / len
        val uy = dy / len
        fun at(d: Float) = floatArrayOf(x1 + ux * minOf(d, len), y1 + uy * minOf(d, len))
        motion(down, MotionEvent.ACTION_DOWN, x1, y1, false, down)
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
        motion(down, MotionEvent.ACTION_MOVE, x2, y2, false, down + DRAG_MS + 10)
        motion(down, MotionEvent.ACTION_MOVE, x2, y2, true, down + back - 5)
        clock.sleep(25)
        val vertical = Math.abs(dy) >= Math.abs(dx)
        motion(down, if (vertical) MotionEvent.ACTION_CANCEL else MotionEvent.ACTION_UP, x2, y2, true)
    }

    private fun density(): Float = try { injector.density } catch (_: Throwable) { 2.75f }

    private fun enterIsNewline(f: Field): Boolean {
        val pkg = f.packageName?.toString()
        val s = editorSettings(pkg) ?: return false
        if (s.pkg != null && pkg != null && s.pkg != pkg) return false
        return s.enterIsNewline
    }

    fun key(code: Int, longPress: Boolean = false): String {
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

    fun typeChecked(text: String, events: Events, waitForFocus: Boolean = false, ci: Boolean = false): Boolean? {
        lastTyping = null
        if (text.isEmpty()) return type(text)
        var initial = focused()
        if (initial == null && waitForFocus && events.awaitCondition(FOCUS_WAIT_MS) { focused() != null }) initial = focused()
        if (initial == null) return null
        val before = fieldState(initial) ?: return null
        val blankNativeField = initial.nativePlainText && before.second.isEmpty() &&
            initial.selectionStart == 0 && initial.selectionEnd == 0
        val typing = Typing(text, before, initial, if (ci) ACK_CI_MS else ACK_MS)
        val end = text.offsetByCodePoints(0, 1)
        val first = text.substring(0, end)
        var seen: Pair<Any, String>? = before
        if (!type(first)) return false
        val sentAt = clock.uptimeMillis()
        var took = events.awaitCondition(FIRST_KEY_MS) { fieldState().also { seen = it } != before }
        if (!took) {
            val pkg = initial.packageName?.toString()
            when (firstKey(before, sentAt, pkg, typing.ackMs, events) { seen = it }) {
                FirstKey.SHOWN -> took = true
                FirstKey.LOST -> {
                    if (!type(first)) return false
                    took = events.awaitCondition(FIRST_KEY_MS) { fieldState().also { seen = it } != before }
                }
                FirstKey.UNPROVEN -> check(!initial.nativePlainText) {
                    "the field did not show the first key (\"$first\") within " +
                        "${FIRST_KEY_MS + typing.ackMs + FIRST_KEY_MS} ms, and its app sent no accessibility event " +
                        "meanwhile that would prove its UI was running (it may be stalled, and show the key later), " +
                        "so the key was not typed again: a key typed again could go in twice"
                }
            }
        }
        if (took) typing.acked[0] = true else typing.rejected[0] = true
        val rest = text.substring(end)
        val sameField = took && initial.nativePlainText && seen?.first == before.first
        val rewrites = sameField && seen?.second != typing.literal(first)
        if (rewrites) {
            typing.mode = "acknowledged"
            if (!typeAcknowledged(typing, 1, events, stopOnFocusMove = false)) return false
        } else {
            val chunkFrom = if (sameField && !typing.cased(0)) probe(typing, events) { formats(initial) } ?: return false else 1
            if (chunkFrom < typing.points.size && !type(text.substring(typing.offset(chunkFrom)))) return false
        }
        if (!took && rest.isNotEmpty() && initial.nativePlainText) took = events.awaitCondition(FIRST_KEY_MS) { fieldState() != before }
        check(took || !initial.nativePlainText) {
            "no field took the text: nothing of it arrived in the focused text field, its first key typed twice " +
                "(the keyboard had not connected to the field yet, after focus moved to it; or the field " +
                "rejects every character typed, e.g. a full max-length field)"
        }
        if (blankNativeField) removeDuplicatePrefix(before.first, text)
        if (initial.nativePlainText) verifyTyped(typing, events)
        lastTyping = if (typing.resent && typing.mode == "acknowledged") "repaired" else typing.mode
        return true
    }

    var lastTyping: String? = null
        private set

    private class Typing(val text: String, before: Pair<Any, String>, field: Field, val ackMs: Int) {
        val identity = before.first
        val pkg: String? = field.packageName?.toString()
        val maxLength: Int = field.maxLength
        val prefix: String
        val suffix: String
        val points: List<String> = buildList {
            var i = 0
            while (i < text.length) { val j = text.offsetByCodePoints(i, 1); add(text.substring(i, j)); i = j }
        }
        val rejected = BooleanArray(points.size)
        val acked = BooleanArray(points.size)
        var mode: String? = null
        var resent = false
        var jumped: String? = null
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

        fun offset(i: Int): Int = points.take(i).sumOf { it.length }

        fun cased(i: Int): Boolean {
            val cp = points[i].codePointAt(0)
            return Character.isLetter(cp) && (Character.toUpperCase(cp) != cp || Character.toLowerCase(cp) != cp)
        }

        fun kind(i: Int): String {
            val cp = points[i].codePointAt(0)
            return when {
                Character.isLetter(cp) -> "letter"
                Character.isDigit(cp) -> "digit"
                Character.isWhitespace(cp) -> "space"
                else -> points[i]
            }
        }

        fun tookKind(i: Int): Boolean = (0 until i).any { acked[it] && kind(it) == kind(i) }

        fun expectedUpTo(i: Int): String = (0..i).filter { !rejected[it] }.joinToString("") { points[it] }

        fun expected(): List<Int> = points.indices.filter { !rejected[it] }

        fun typedPart(value: String): String? {
            if (value.length < prefix.length + suffix.length) return null
            if (!value.startsWith(prefix, ignoreCase = true) || !value.endsWith(suffix, ignoreCase = true)) return null
            return value.substring(prefix.length, value.length - suffix.length)
        }

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

        fun lastArrived(typed: String): Boolean {
            val last = expected().lastOrNull() ?: return true
            return typed.endsWith(points[last], ignoreCase = true)
        }

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

        fun lostKeys(typed: String): Boolean {
            if (!short(typed) || !lastArrived(typed)) return false
            return missing(typed).any { acked[it] }
        }

        fun matches(typed: String): Boolean = expected().joinToString("") { points[it] }.equals(typed, ignoreCase = true)
    }

    private fun typeAcknowledged(typing: Typing, from: Int, events: Events, stopOnFocusMove: Boolean): Boolean {
        for (i in from until typing.points.size) {
            val prev = fieldState() ?: awaitReadable(typing, i, events)
            if (prev?.first != typing.identity) {
                typing.focusMoved = true
                if (stopOnFocusMove) return true
            }
            val max = typing.maxLength
            if (prev != null && prev.first == typing.identity && max > 0 && prev.second.length >= max) {
                for (j in i until typing.points.size) typing.rejected[j] = true
                return true
            }
            if (!type(typing.points[i])) return false
            val wait = awaitKey(prev, clock.uptimeMillis(), typing, events)
            var changed = wait == KeyWait.SHOWN
            if (wait == KeyWait.REJECTED_LIVE && !stopOnFocusMove && prev?.first == typing.identity && typing.tookKind(i)) {
                when (resend(typing, i, prev, events)) {
                    Resend.SHOWN -> changed = true
                    Resend.FULL -> {
                        for (j in i until typing.points.size) typing.rejected[j] = true
                        return true
                    }
                    Resend.NOT_TYPED -> return false
                }
            }
            typing.acked[i] = changed
            typing.rejected[i] = !changed
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

    private fun resend(typing: Typing, i: Int, prev: Pair<Any, String>, events: Events): Resend {
        val key = typing.points[i]
        if (fieldState() != prev) return Resend.SHOWN
        if (!type(key)) return Resend.NOT_TYPED
        var wait = awaitKey(prev, clock.uptimeMillis(), typing, events)
        val was = typing.typedPart(prev.second)
        if (wait != KeyWait.SHOWN && fieldState() != prev) wait = KeyWait.SHOWN
        if (wait != KeyWait.SHOWN) {
            val next = i + 1
            if (wait != KeyWait.REJECTED_LIVE) stalled(typing, i, prev)
            if (next >= typing.points.size) return fullAfterHold(typing, i, prev, events)
            if (!type(typing.points[next])) return Resend.NOT_TYPED
            val nextWait = awaitKey(prev, clock.uptimeMillis(), typing, events)
            if (nextWait == KeyWait.REJECTED_LIVE && fieldState() == prev) return fullAfterHold(typing, next, prev, events)
            if (nextWait != KeyWait.SHOWN && fieldState() == prev) stalled(typing, next, prev)
            val holds = fieldState()?.second
            throw IllegalStateException(
                "the field did not take \"$key\" (key ${i + 1} of ${typing.points.size} of \"${typing.text}\"): it " +
                    "takes keys of its kind, showed nothing of this one, sent twice, and then took the key after it; " +
                    "it holds \"${holds ?: prev.second}\", expected \"${typing.literal(typing.text)}\""
            )
        }
        val shown = fieldState()
        val now = shown?.takeIf { it.first == typing.identity }?.let { typing.typedPart(it.second) }
        val exact = was != null && now != null && now.equals(was + key, ignoreCase = true)
        val moved = exact && awaitField(typing.ackMs, events) { fieldState() != shown }
        check(exact && !moved) {
            val holds = fieldState()?.second ?: shown?.second.orEmpty()
            "typing lost keys: \"$key\" (key ${i + 1} of ${typing.points.size}) did not show, was typed again once, " +
                "and the field then shows \"$holds\", not the text before it plus that key" +
                (if (moved) " (it changed again after: the key that did not show came late)" else "") +
                "; expected \"${typing.literal(typing.text)}\""
        }
        typing.resent = true
        return Resend.SHOWN
    }

    private fun fullAfterHold(typing: Typing, i: Int, prev: Pair<Any, String>, events: Events): Resend {
        val moved = awaitField(typing.ackMs, events) { fieldState() != prev }
        check(!moved) {
            "typing lost keys: \"${typing.points[i]}\" (key ${i + 1} of ${typing.points.size}) did not show, sent " +
                "again, and the field changed after: it holds \"${fieldState()?.second.orEmpty()}\", expected " +
                "\"${typing.literal(typing.text)}\""
        }
        return Resend.FULL
    }

    private fun stalled(typing: Typing, i: Int, prev: Pair<Any, String>): Nothing =
        throw IllegalStateException(
            "the app stopped responding while typing: \"${typing.points[i]}\" (key ${i + 1} of ${typing.points.size} " +
                "of \"${typing.text}\") did not show after a key typed again, and the app sent no event that it was " +
                "running; it holds \"${fieldState()?.second ?: prev.second}\", expected \"${typing.literal(typing.text)}\""
        )

    private enum class Resend { SHOWN, FULL, NOT_TYPED }

    private enum class KeyWait {
        SHOWN,
        REJECTED_LIVE,
        REJECTED,
    }

    private fun probe(typing: Typing, events: Events, formats: () -> Boolean): Int? {
        var formatter: Boolean? = null
        fun formatter(): Boolean = formatter ?: formats().also { formatter = it }
        for (i in 1 until typing.points.size) {
            if ((i until typing.points.size).none { typing.cased(it) } && !formatter()) return i
            val prev = fieldState()
            if (prev?.first != typing.identity) return i
            if (!type(typing.points[i])) return null
            val changed = awaitFieldChange(prev, typing.ackMs, events)
            typing.acked[i] = changed
            typing.rejected[i] = !changed
            if (!changed) {
                if (formatter()) continue
                return i + 1
            }
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

    private fun formats(f: Field): Boolean {
        val type = f.inputType.takeIf { it != 0 } ?: run {
            val pkg = f.packageName?.toString() ?: return false
            val s = editorSettings(pkg) ?: return false
            if (s.pkg != pkg) return false
            s.inputType
        }
        return when (type and InputType.TYPE_MASK_CLASS) {
            InputType.TYPE_CLASS_PHONE, InputType.TYPE_CLASS_NUMBER, InputType.TYPE_CLASS_DATETIME -> true
            else -> false
        }
    }

    private enum class FirstKey { SHOWN, LOST, UNPROVEN }

    private fun firstKey(
        before: Pair<Any, String>, sentAt: Long, pkg: String?, spanMs: Int, events: Events,
        seen: (Pair<Any, String>?) -> Unit,
    ): FirstKey {
        var liveFrom = 0L
        var result = FirstKey.UNPROVEN
        awaitField(FIRST_KEY_MS + spanMs, events) {
            val now = fieldState()
            if (now != before) {
                seen(now)
                result = FirstKey.SHOWN
                return@awaitField true
            }
            val event = events.lastAppEventAt(pkg)
            if (liveFrom == 0L && event >= sentAt) liveFrom = event
            if (liveFrom != 0L && clock.uptimeMillis() - liveFrom >= spanMs) result = FirstKey.LOST
            result == FirstKey.LOST
        }
        return result
    }

    private fun awaitKey(prev: Pair<Any, String>?, sentAt: Long, typing: Typing, events: Events): KeyWait {
        var eventFrom = 0L
        var frameFrom = 0L
        var result = KeyWait.REJECTED
        awaitField(typing.ackMs + FIRST_KEY_MS, events) {
            if (fieldState() != prev) {
                result = KeyWait.SHOWN
                return@awaitField true
            }
            val event = events.lastAppEventAt(typing.pkg)
            if (eventFrom == 0L && event >= sentAt) eventFrom = event
            val frame = events.lastFrameAt()
            if (frameFrom == 0L && frame >= sentAt) frameFrom = frame
            val at = clock.uptimeMillis()
            fun past(from: Long) = from != 0L && at - from >= typing.ackMs && at - sentAt >= typing.ackMs
            when {
                past(eventFrom) -> { result = KeyWait.REJECTED_LIVE; true }
                past(frameFrom) -> true
                else -> false
            }
        }
        return result
    }

    private fun awaitReadable(typing: Typing, i: Int, events: Events): Pair<Any, String>? {
        val since = clock.uptimeMillis()
        var state: Pair<Any, String>? = null
        val woke = awaitField(FIRST_KEY_MS, events) {
            state = fieldState()
            state != null || events.lastAppEventAt(typing.pkg) >= since
        }
        check(woke) {
            "the app stopped responding while typing: its focused field could not be read for $FIRST_KEY_MS ms, " +
                "and it sent no event, before \"${typing.points[i]}\" (key ${i + 1} of ${typing.points.size}) " +
                "of \"${typing.text}\""
        }
        return state ?: fieldState()
    }

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

    private fun verifyTyped(typing: Typing, events: Events) {
        if (typing.mode != "acknowledged" || typing.focusMoved) return
        fun typedNow(): String? {
            val (id, value) = fieldState() ?: return null
            return if (id == typing.identity) typing.typedPart(value) else null
        }
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
        var typed: String = verdict() ?: return
        var placed = false
        for (attempt in 0..1) {
            val f = focused() ?: return
            val cursor = typing.prefix.length + typed.length
            placed = f.selectionStart == cursor && f.selectionEnd == cursor || f.select(cursor, cursor)
            if (!placed || typedNow() == typed) break
            if (attempt == 1) return
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
        if (!text.startsWith(extra) || !field.setText(text)) return
        field.select(text.length, text.length)
    }

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

    private enum class RepairDel { SHOWN, FOCUS_LEFT, MOVED, STALLED }

    private fun repairDel(identity: Any, ackMs: Int, events: Events, seen: (String) -> Boolean): RepairDel {
        val prev = fieldState()
        if (prev?.first != identity) return RepairDel.FOCUS_LEFT
        if (!seen(prev.second)) return RepairDel.MOVED
        injectKeys(listOf(DEL))
        return if (awaitFieldChange(prev, ackMs, events)) RepairDel.SHOWN else RepairDel.STALLED
    }

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

    fun erase(count: Int, events: Events, ci: Boolean = false) {
        lastErase = null
        val n = count.coerceIn(0, 10_000)
        if (n == 0) return
        val f = focused()
        val before = fieldState(f)
        val erasing = if (f != null && f.nativePlainText && before != null) Erasing(before, n, if (ci) ACK_CI_MS else ACK_MS) else null
        cursorToEnd(f, events, if (ci) ACK_CI_MS else ACK_MS)
        injectKeys(List(n) { DEL })
        if (erasing != null) verifyErased(erasing, events)
    }

    var lastErase: String? = null
        private set

    private class Erasing(before: Pair<Any, String>, val count: Int, val ackMs: Int) {
        val identity = before.first
        val old = before.second
        val expected: String = old.substring(0, graphemeCut(old, count))
        private val longest = old.substring(0, codePointCut(old, count))

        fun done(value: String) = value.length in expected.length..longest.length && old.startsWith(value)

        fun judged(value: String): Boolean {
            if (value.length > expected.length && old.startsWith(value)) return true
            val k = old.length - value.length
            if (k <= 0) return false
            var p = 0
            while (p < value.length && old[p] == value[p]) p++
            var s = 0
            while (s < value.length && old[old.length - 1 - s] == value[value.length - 1 - s]) s++
            return maxOf(k, old.length - s) <= minOf(p + k, old.length - 1)
        }
    }

    private fun verifyErased(e: Erasing, events: Events) {
        fun now(): String? {
            val (id, value) = fieldState() ?: return null
            return if (id == e.identity) value else null
        }
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
        val left = settled() ?: return
        if (e.done(left) || !stable || !e.judged(left)) return
        val f = focused() ?: return
        if (fieldState(f)?.first != e.identity) return
        if (now() != left) return
        lastErase = "repaired"
        var lostFocus = false
        val took: Boolean
        if (e.expected.isNotEmpty() && left.startsWith(e.expected)) {
            cursorToEnd(f, events, e.ackMs)
            val (lost, shown) = eraseBack(e, left, minOf(left.length - e.expected.length, e.count), events, ::now)
            lostFocus = lost
            took = shown
        } else {
            took = f.setText(e.expected)
        }
        check(!lostFocus) {
            "erasing: the focus left the field while what was left was deleted: it held \"$left\", expected \"${e.expected}\""
        }
        val leftover = e.expected.isNotEmpty() && left.startsWith(e.expected)
        if (leftover && !took) { lastErase = null; return }
        val end = settled()
        if (end != null && e.done(end)) return
        if (end == null || !stable || took && end == left) { lastErase = null; return }
        check(false) {
            "erasing: the field holds \"$end\", expected \"${e.expected}\"" +
                if (!took) " (its caret was put at the end and the DELs sent; setting the text it should hold was refused)"
                else " (its caret was put at the end, the DELs sent, and the field put right once)"
        }
    }

    private fun cursorToEnd(node: Field? = focused(), events: Events? = null, capMs: Int = 0) {
        if (node != null && node.editable) {
            val len = if (node.showingHint) 0 else node.text?.length ?: 0
            val reports = node.selectionStart >= 0 && node.selectionEnd >= 0
            if (node.select(len, len)) {
                if (events != null && reports) {
                    awaitField(capMs, events) { focused()?.let { it.selectionStart == len && it.selectionEnd == len } ?: true }
                }
                return
            }
        }
        key(KeyEvent.KEYCODE_MOVE_END)
    }

    fun clearFocused(): Boolean {
        val node = focused() ?: return false
        if (!node.editable) return false
        return node.setText("")
    }

    companion object {
        const val FRAME_MS = 16L
        const val DOUBLE_TAP_CONTACT_MS = 100L
        const val DOUBLE_TAP_GAP_MS = 80L
        const val DOUBLE_TAP_PAUSE_MS = 120L
        const val DRAG_MS = 50L
        const val DRAG_REST_MS = 60L
        const val FRAME_STEP_MS = 8L
        const val HOLD_DRAG_STEP_DP = 20.0
        const val HOLD_DRAG_DROP_MS = 300L
        const val HOLD_DRAG_SETTLE_DP = 6.0
        const val HOLD_DRAG_SETTLE_FRAMES = 3L
        const val HOLD_DRAG_MIN_MOVE_MS = 100L
        const val KEY_CHUNK = 16
        const val FOCUS_WAIT_MS = 1000
        const val FIRST_KEY_MS = 1000
        const val ACK_MS = 300
        const val ACK_CI_MS = 1000
        const val STABLE_MS = 2 * FRAME_MS
        const val VERDICT_ROUNDS = 3

        const val SETTLE_CAPS = 3

        private val DEL = listOf(Key(KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DEL), Key(KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DEL))

        fun codePointCut(s: String, count: Int): Int {
            var at = s.length
            var n = count
            while (n-- > 0 && at > 0) at = s.offsetByCodePoints(at, -1)
            return at
        }

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
