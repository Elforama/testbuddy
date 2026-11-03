package dev.testbuddy.agent

import android.app.UiAutomation
import android.os.Bundle
import android.os.SystemClock
import android.util.Log
import android.view.InputDevice
import android.view.InputEvent
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.accessibility.AccessibilityNodeInfo
import java.lang.reflect.Method

/**
 * Injects touches and keys. The shell uid holds INJECT_EVENTS, so events go straight to the
 * input manager (as scrcpy does) instead of through UiAutomation, which syncs input
 * transactions around every DOWN/UP (~30 ms each). Falls back to UiAutomation if the hidden
 * API is missing.
 */
class Input(private val ua: UiAutomation, private val tree: Tree) {
    private val manager: Any?
    private val injectMethod: Method?
    private var useManager: Boolean

    init {
        var m: Any? = null
        var method: Method? = null
        try {
            val cls = try {
                Class.forName("android.hardware.input.InputManagerGlobal") // Android 14+
            } catch (_: ClassNotFoundException) {
                android.hardware.input.InputManager::class.java
            }
            m = cls.getDeclaredMethod("getInstance").invoke(null)
            method = cls.getMethod("injectInputEvent", InputEvent::class.java, Integer.TYPE)
        } catch (t: Throwable) {
            Log.w(TAG, "InputManager injection unavailable, using UiAutomation: ${cause(t)}")
        }
        manager = m
        injectMethod = method
        useManager = m != null && method != null
    }

    val path: String get() = if (useManager) "input-manager" else "ui-automation"

    fun inject(e: InputEvent, sync: Boolean) {
        if (useManager) {
            try {
                val mode = if (sync) 2 /* WAIT_FOR_FINISH */ else 0 /* ASYNC */
                injectMethod!!.invoke(manager, e, mode)
                return
            } catch (t: Throwable) {
                Log.w(TAG, "InputManager injection failed, switching to UiAutomation: ${cause(t)}")
                useManager = false
            }
        }
        ua.injectInputEvent(e, sync)
    }

    /** The latest event time injected: a gesture's back-dated DOWN must not go before it. */
    private var lastEventAt = 0L

    private fun motion(down: Long, action: Int, x: Float, y: Float, sync: Boolean, at: Long = SystemClock.uptimeMillis()) {
        lastEventAt = maxOf(lastEventAt, at)
        val e = MotionEvent.obtain(down, at, action, x, y, 0)
        e.source = InputDevice.SOURCE_TOUCHSCREEN
        inject(e, sync)
        e.recycle()
    }

    fun tap(x: Float, y: Float) {
        val down = SystemClock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x, y, false, down)
        motion(down, MotionEvent.ACTION_UP, x, y, true)
    }

    fun doubleTap(x: Float, y: Float) {
        tap(x, y)
        Thread.sleep(80) // inside the double-tap timeout (300 ms), past the min gap (40 ms)
        tap(x, y)
    }

    fun press(x: Float, y: Float, holdMs: Long) {
        val down = SystemClock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x, y, true, down)
        Thread.sleep(holdMs) // the app's long-press timer runs in real time
        motion(down, MotionEvent.ACTION_UP, x, y, true)
    }

    /** A straight-line drag in [durationMs], a move every frame. */
    fun swipe(x1: Float, y1: Float, x2: Float, y2: Float, durationMs: Long) {
        val down = SystemClock.uptimeMillis()
        motion(down, MotionEvent.ACTION_DOWN, x1, y1, false, down)
        val steps = maxOf(2, (durationMs / FRAME_MS).toInt())
        val start = SystemClock.uptimeMillis()
        for (i in 1..steps) {
            val frac = i.toFloat() / steps
            val target = start + (durationMs * frac).toLong()
            val wait = target - SystemClock.uptimeMillis()
            if (wait > 0) Thread.sleep(wait) // gesture pacing: the app derives velocity from it
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
        val gap = lastEventAt - (SystemClock.uptimeMillis() - back) + 1
        if (gap > 0) Thread.sleep(gap)
        val down = SystemClock.uptimeMillis() - back
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
        Thread.sleep(25) // the end lands in a later frame than the moves
        val vertical = Math.abs(dy) >= Math.abs(dx)
        motion(down, if (vertical) MotionEvent.ACTION_CANCEL else MotionEvent.ACTION_UP, x2, y2, true)
    }

    private fun density(): Float = try {
        android.content.res.Resources.getSystem().displayMetrics.density
    } catch (_: Throwable) {
        2.75f
    }

    fun key(code: Int, longPress: Boolean = false) {
        val t = SystemClock.uptimeMillis()
        inject(KeyEvent(t, t, KeyEvent.ACTION_DOWN, code, 0).also { it.source = InputDevice.SOURCE_KEYBOARD }, false)
        if (longPress) {
            Thread.sleep(600)
            inject(KeyEvent(t, SystemClock.uptimeMillis(), KeyEvent.ACTION_DOWN, code, 1, 0, KeyCharacterMap.VIRTUAL_KEYBOARD, 0, KeyEvent.FLAG_LONG_PRESS, InputDevice.SOURCE_KEYBOARD), false)
        }
        inject(KeyEvent(t, SystemClock.uptimeMillis(), KeyEvent.ACTION_UP, code, 0).also { it.source = InputDevice.SOURCE_KEYBOARD }, true)
    }

    private val keyMap by lazy { KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD) }

    /**
     * Types text as key presses, so the app sees what a keyboard sends. Characters with no
     * key (emoji, most non-Latin scripts) go in through ACTION_SET_TEXT on the focused field.
     */
    fun type(text: String): Boolean {
        val presses = ArrayList<Array<KeyEvent>>()
        for (ch in text) {
            val events = keyMap.getEvents(charArrayOf(ch))
            if (events == null) return insertText(text)
            presses += events
        }
        injectKeys(presses)
        return true
    }

    /**
     * Keys sent in chunks, each key stamped as it is sent and each chunk's last event waited
     * for. The input dispatcher drops key events older than 10 s, so stamping a long text all
     * at once loses its end on a slow device.
     */
    private fun injectKeys(presses: List<Array<KeyEvent>>) {
        var next = 0
        while (next < presses.size) {
            val end = minOf(next + KEY_CHUNK, presses.size)
            for (k in next until end) {
                val down = SystemClock.uptimeMillis()
                presses[k].forEachIndexed { j, e ->
                    val sent = KeyEvent(down, SystemClock.uptimeMillis(), e.action, e.keyCode, e.repeatCount,
                        e.metaState, e.deviceId, e.scanCode, e.flags, InputDevice.SOURCE_KEYBOARD)
                    inject(sent, k == end - 1 && j == presses[k].lastIndex)
                }
            }
            next = end
        }
    }

    private fun insertText(text: String): Boolean {
        val node = tree.focusedInput() ?: return false
        if (!node.isEditable) return false
        val current = if (node.isShowingHintText) "" else node.text?.toString() ?: ""
        var start = node.textSelectionStart
        var end = node.textSelectionEnd
        if (start !in 0..current.length || end !in 0..current.length) { start = current.length; end = current.length }
        if (start > end) start = end.also { end = start }
        val args = Bundle()
        args.putCharSequence(
            AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE,
            current.substring(0, start) + text + current.substring(end),
        )
        if (!node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)) return false
        val sel = Bundle()
        sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_START_INT, start + text.length)
        sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_END_INT, start + text.length)
        node.performAction(AccessibilityNodeInfo.ACTION_SET_SELECTION, sel)
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
        val del = arrayOf(
            KeyEvent(0, 0, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DEL, 0),
            KeyEvent(0, 0, KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DEL, 0),
        )
        injectKeys(List(count.coerceIn(0, 10_000)) { del })
    }

    /** Moves the focused field's cursor to the end of its text (selection action, else the End key). */
    private fun cursorToEnd() {
        val node = tree.focusedInput()
        if (node != null && node.isEditable) {
            val len = if (node.isShowingHintText) 0 else node.text?.length ?: 0
            if (node.textSelectionStart == len && node.textSelectionEnd == len) return
            val sel = Bundle()
            sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_START_INT, len)
            sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_END_INT, len)
            if (node.performAction(AccessibilityNodeInfo.ACTION_SET_SELECTION, sel)) return
        }
        key(KeyEvent.KEYCODE_MOVE_END)
    }

    /** Clears the focused field in one action when possible. */
    fun clearFocused(): Boolean {
        val node = tree.focusedInput() ?: return false
        if (!node.isEditable) return false
        val args = Bundle()
        args.putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, "")
        return node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)
    }

    companion object {
        const val FRAME_MS = 16L
        /** A no-fling drag: how long the finger seems to take, then rest before it ends. */
        const val DRAG_MS = 50L
        const val DRAG_REST_MS = 60L
        const val FRAME_STEP_MS = 8L
        const val KEY_CHUNK = 16
    }
}
