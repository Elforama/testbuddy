package dev.testbuddy.agent

/*
 * What the agent's logic needs from Android, as interfaces. The logic (settling, gestures,
 * request handling, the screen's JSON) uses only these, so it runs on the JVM in unit tests
 * against fakes (app/src/test/.../Fakes.kt) and on the device against AndroidPlatform.kt.
 * Android's int constants (event types, key codes, window types) are compile-time constants,
 * so the logic keeps using them: they need no device.
 */

/** Time and timers. Uptime ms is SystemClock.uptimeMillis() on the device. */
interface Clock {
    fun uptimeMillis(): Long
    /** CLOCK_MONOTONIC ns (System.nanoTime): the clock of window frame stats. */
    fun nanoTime(): Long
    /** Blocks for [ms]: gesture pacing, which the app reads as a finger's speed. */
    fun sleep(ms: Long)
    /** `lock.wait(ms)`, called with [lock] held. It may return early: callers re-check. */
    fun await(lock: Object, ms: Long)
    /** Runs [task] on the agent's timer thread [delayMs] from now. */
    fun postDelayed(task: Runnable, delayMs: Long)
    /** Runs [task] on the agent's timer thread at uptime [uptimeMs]. */
    fun postAt(task: Runnable, uptimeMs: Long)
    fun cancel(task: Runnable)
}

/** An accessibility event, as [Events] reads it. */
class UiEvent(
    /** AccessibilityEvent.TYPE_*. */
    val type: Int,
    val pkg: String?,
    val className: String?,
    /** AccessibilityEvent.CONTENT_CHANGE_TYPE_* bits. */
    val contentChangeTypes: Int = 0,
    /** The source view (window id and node id combined), null when unknown. */
    val source: Long? = null,
    /** The event's texts (a Toast's message). */
    val texts: List<String> = emptyList(),
)

/** What [Events] reads from the device besides the events themselves. */
interface UiProbe {
    /** The active window's id, -1 when there is none. */
    fun activeWindowId(): Int
    /** The package that owns the active window, null when unknown. */
    fun activePackage(): String?
    /** Starts [window]'s frame stats over. */
    fun clearFrameStats(window: Int)
    /** When [window] presented its latest frame since [clearFrameStats] (CLOCK_MONOTONIC ns), 0 if none. */
    fun lastFramePresentedNanos(window: Int): Long
    /** `settings get [ns] [key]`, null when unset. */
    fun setting(ns: String, key: String): String?
}

/** A touch event to inject. [action] is MotionEvent.ACTION_*; times are uptime ms. */
data class Touch(val action: Int, val downAt: Long, val at: Long, val x: Float, val y: Float)

/** A key event to inject: KeyEvent's fields. [action] is KeyEvent.ACTION_*; times are uptime ms. */
data class Key(
    val action: Int,
    val code: Int,
    val downAt: Long = 0,
    val at: Long = 0,
    val repeat: Int = 0,
    val meta: Int = 0,
    val deviceId: Int = android.view.KeyCharacterMap.VIRTUAL_KEYBOARD,
    val scanCode: Int = 0,
    val flags: Int = 0,
)

/** Puts input events into the device. */
interface Injector {
    /** Waits for the platform's input windows before an unguarded, paced swipe. */
    fun prepareSwipe() {}
    /** How events go in, reported to the host: "input-manager" or "ui-automation". */
    val path: String
    /** With [sync], returns once the event was handled. */
    fun touch(t: Touch, sync: Boolean)
    fun key(k: Key, sync: Boolean)
    /** The key events that type [ch] on the virtual keyboard, null when no key does. */
    fun keysFor(ch: Char): List<Key>?
    /** Screen density (px per dp). */
    val density: Float
}

/** The text field with input focus. */
interface Field {
    /** Stable across fresh reads, different when input focus moves (e.g. OTP boxes). */
    val identity: Any get() = this
    val editable: Boolean
    val showingHint: Boolean
    val nativePlainText: Boolean get() = false
    val text: CharSequence?
    val selectionStart: Int
    val selectionEnd: Int
    /** ACTION_SET_TEXT. */
    fun setText(text: CharSequence): Boolean
    /** ACTION_SET_SELECTION. */
    fun select(start: Int, end: Int): Boolean
    /** Perform an explicitly advertised IME action, false if unavailable/not handled. */
    fun submitIme(): Boolean = false
    /** Whether [submitIme] could act (without acting); true when the field cannot tell. */
    fun canSubmitIme(): Boolean = true
    /** The app the field belongs to, null when unknown. */
    val packageName: CharSequence? get() = null
    /** Identify the focused editor for an input-connection request, without editing. */
    fun editorTarget(): EditorTarget? = null
}

/** A rectangle in screen pixels. */
data class Box(val left: Int, val top: Int, val right: Int, val bottom: Int) {
    val width: Int get() = right - left
    val height: Int get() = bottom - top
    val centerX: Int get() = (left + right) shr 1
    val isEmpty: Boolean get() = left >= right || top >= bottom

    /** The smallest box holding both (Rect.union: an empty box adds nothing). */
    fun union(o: Box): Box = when {
        o.isEmpty -> this
        isEmpty -> o
        else -> Box(minOf(left, o.left), minOf(top, o.top), maxOf(right, o.right), maxOf(bottom, o.bottom))
    }

    companion object { val EMPTY = Box(0, 0, 0, 0) }
}

/** An accessibility node, as [Tree] writes it. */
interface UiNode {
    val bounds: Box
    val className: CharSequence?
    val packageName: CharSequence?
    val windowId: Int
    /** Its window's type (AccessibilityWindowInfo.TYPE_*), -1 when not known. Asked only for
     *  an active root its window list does not have (issue #135). */
    val windowType: Int get() = -1
    val text: CharSequence?
    val contentDescription: CharSequence?
    val viewId: String?
    val hint: CharSequence?
    val error: CharSequence?
    /** Android 11+, null before. */
    val stateDescription: CharSequence?
    val clickable: Boolean
    val enabled: Boolean
    val focused: Boolean
    val scrollable: Boolean
    val checked: Boolean
    val selected: Boolean
    val checkable: Boolean
    val password: Boolean
    val editable: Boolean
    val longClickable: Boolean
    val focusable: Boolean
    val showingHint: Boolean
    val visibleToUser: Boolean
    /** Offers a scroll forward / down / right action. */
    val canScrollForward: Boolean
    /** Offers a scroll backward / up / left action. */
    val canScrollBackward: Boolean
    /** A list's rows and columns, null when it is no list. */
    val collection: IntArray?
    /** An item's row and column in its list, null when it is no item. */
    val collectionItem: IntArray?
    /** Its place among its siblings as drawn (later is on top; elevation and custom drawing
     *  order count), or -1 when not known. */
    val drawingOrder: Int
    val childCount: Int
    /** The child at [i], null when it cannot be read (gone meanwhile). */
    fun child(i: Int): UiNode?
}

/** An accessibility window. [type] is AccessibilityWindowInfo.TYPE_*. */
interface UiWindow {
    val id: Int
    val type: Int
    val layer: Int
    val active: Boolean
    val focused: Boolean
    val title: CharSequence?
    val bounds: Box
    /** The window's root node, null when it cannot be read. */
    fun root(): UiNode?
}

/** The accessibility tree. */
interface Nodes {
    /** The windows on screen, top-most first; empty when they cannot be read. */
    fun windows(prefetchChildren: Boolean = false): List<UiWindow>
    /** The active window's root, when the windows list is empty. */
    fun activeRoot(prefetchChildren: Boolean = false): UiNode?
    /** Drops the node cache, so the next read asks the apps again. */
    fun clearCache()
    /** The node with input focus, as a field. */
    fun focusedInput(): Field?
}

/** A screenshot, encoded. */
class Shot(val width: Int, val height: Int, val format: String, val bytes: ByteArray)

/** What a shell command printed and how it ended. [raw] is the output's bytes, for binary output. */
class ShellResult(val code: Int, val out: String, val timedOut: Boolean, val raw: ByteArray = out.toByteArray())

/** Turns a rotate request asks for. */
enum class Rotation { PORTRAIT, LANDSCAPE_LEFT, LANDSCAPE_RIGHT, UPSIDE_DOWN, FREE;
    companion object {
        /** The protocol's names (docs/protocol.md); anything else unfreezes rotation. */
        fun of(name: String): Rotation = when (name) {
            "landscape", "landscapeLeft" -> LANDSCAPE_LEFT
            "landscapeRight" -> LANDSCAPE_RIGHT
            "upsideDown" -> UPSIDE_DOWN
            "portrait" -> PORTRAIT
            else -> FREE
        }
    }
}

/** The device itself: its display, its shell, and the agent's process. */
interface Device {
    val sdk: Int
    val density: Double
    /** The display size in pixels, as the screen is turned now: [w, h]. */
    fun displaySize(): IntArray
    /** Surface.ROTATION_* (0 to 3). */
    fun rotation(): Int
    fun rotate(to: Rotation)
    /** The package that owns the active window, null when unknown. */
    fun foregroundPackage(): String?
    /** [format] is "png" or "jpeg"; [scale] in 0.05..0.99 shrinks it. */
    fun screenshot(scale: Double, format: String, quality: Int): Shot
    fun shell(cmd: String, timeoutMs: Int): ShellResult
    /** Ends the agent: takes NoopService out and exits, on a thread of its own. */
    fun exitSoon()
}
