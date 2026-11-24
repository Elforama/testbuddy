package dev.testbuddy.agent

interface Clock {
    fun uptimeMillis(): Long
    fun nanoTime(): Long
    fun sleep(ms: Long)
    fun await(lock: Object, ms: Long)
    fun postDelayed(task: Runnable, delayMs: Long)
    fun postAt(task: Runnable, uptimeMs: Long)
    fun cancel(task: Runnable)
}

class UiEvent(
    val type: Int,
    val pkg: String?,
    val className: String?,
    val contentChangeTypes: Int = 0,
    val source: Long? = null,
    val texts: List<String> = emptyList(),
    val eventTime: Long = 0,
    val windowId: Int = -1,
)

interface UiProbe {
    fun activeWindowId(): Int
    fun activePackage(): String?
    fun clearFrameStats(window: Int)
    fun lastFramePresentedNanos(window: Int): Long
    fun setting(ns: String, key: String): String?
    fun windowIds(): IntArray? = null
    fun windowBounds(): Map<Int, Box>? = windowIds()?.associateWith { Box(0, 0, 0, 0) }
}

data class Touch(val action: Int, val downAt: Long, val at: Long, val x: Float, val y: Float)

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

interface Injector {
    fun prepareSwipe() {}
    val path: String
    fun touch(t: Touch, sync: Boolean): Boolean
    fun key(k: Key, sync: Boolean)
    fun keysFor(ch: Char): List<Key>?
    val density: Float
}

interface Field {
    val identity: Any get() = this
    val editable: Boolean
    val showingHint: Boolean
    val nativePlainText: Boolean get() = false
    val inputType: Int get() = 0
    val maxLength: Int get() = -1
    val text: CharSequence?
    val selectionStart: Int
    val selectionEnd: Int
    fun setText(text: CharSequence): Boolean
    fun select(start: Int, end: Int): Boolean
    fun submitIme(): Boolean = false
    fun canSubmitIme(): Boolean = true
    val packageName: CharSequence? get() = null
    fun editorTarget(): EditorTarget? = null
}

data class Box(val left: Int, val top: Int, val right: Int, val bottom: Int) {
    val width: Int get() = right - left
    val height: Int get() = bottom - top
    val centerX: Int get() = (left + right) shr 1
    val isEmpty: Boolean get() = left >= right || top >= bottom

    fun union(o: Box): Box = when {
        o.isEmpty -> this
        isEmpty -> o
        else -> Box(minOf(left, o.left), minOf(top, o.top), maxOf(right, o.right), maxOf(bottom, o.bottom))
    }

    companion object { val EMPTY = Box(0, 0, 0, 0) }
}

interface UiNode {
    val bounds: Box
    val className: CharSequence?
    val packageName: CharSequence?
    val windowId: Int
    val windowType: Int get() = -1
    val text: CharSequence?
    val contentDescription: CharSequence?
    val viewId: String?
    val hint: CharSequence?
    val error: CharSequence?
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
    val canScrollForward: Boolean
    val canScrollBackward: Boolean
    val collection: IntArray?
    val collectionItem: IntArray?
    val drawingOrder: Int
    val childCount: Int
    fun child(i: Int): UiNode?
    fun refresh(): Boolean
}

interface UiWindow {
    val id: Int
    val type: Int
    val layer: Int
    val active: Boolean
    val focused: Boolean
    val title: CharSequence?
    val bounds: Box
    fun root(): UiNode?
}

interface Nodes {
    fun windows(prefetchChildren: Boolean = false): List<UiWindow>
    fun activeRoot(prefetchChildren: Boolean = false): UiNode?
    fun clearCache()
    fun focusedInput(): Field?
}

class Shot(val width: Int, val height: Int, val format: String, val bytes: ByteArray)

class ShellResult(val code: Int, val out: String, val timedOut: Boolean, val raw: ByteArray = out.toByteArray())

enum class Rotation { PORTRAIT, LANDSCAPE_LEFT, LANDSCAPE_RIGHT, UPSIDE_DOWN, FREE;
    companion object {
        fun of(name: String): Rotation = when (name) {
            "landscape", "landscapeLeft" -> LANDSCAPE_LEFT
            "landscapeRight" -> LANDSCAPE_RIGHT
            "upsideDown" -> UPSIDE_DOWN
            "portrait" -> PORTRAIT
            else -> FREE
        }
    }
}

interface Device {
    val sdk: Int
    val density: Double
    fun displaySize(): IntArray
    fun rotation(): Int
    fun rotate(to: Rotation)
    fun foregroundPackage(): String?
    fun screenshot(scale: Double, format: String, quality: Int): Shot
    fun shell(cmd: String, timeoutMs: Int): ShellResult
    fun dump(service: String, args: List<String>, timeoutMs: Int): ShellResult? = null
    fun exitSoon()
    fun isActivity(pkg: String, cls: String): Boolean? = null
}
