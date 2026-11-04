package dev.testbuddy.agent

import android.view.KeyEvent

/*
 * Fakes of Platform.kt's interfaces: the agent's logic runs on them in unit tests, with no
 * device. Time is the fake clock's: nothing waits for real, so tests are quick and exact.
 */

/**
 * A clock that moves only when the code under test waits or sleeps (or a test calls
 * [advance]), running the timers that come due on the way. Single-threaded: a wait runs
 * the timers (which may change what the waiter waits for) and returns, as a notify would.
 */
class FakeClock(var now: Long = 0) : Clock {
    private class Task(val at: Long, val seq: Int, val task: Runnable)
    private val tasks = ArrayList<Task>()
    private var nextSeq = 0

    override fun uptimeMillis(): Long = now
    override fun nanoTime(): Long = now * 1_000_000
    override fun sleep(ms: Long) = advance(ms)

    override fun await(lock: Object, ms: Long) {
        // lock.wait(0) waits until notified, with no time limit: a caller computing 0 hangs.
        require(ms > 0) { "lock.wait($ms): ${if (ms == 0L) "waits forever" else "negative timeout"}" }
        val next = tasks.minOfOrNull { it.at } ?: Long.MAX_VALUE
        advanceTo(minOf(now + ms, maxOf(next, now)))
    }

    override fun postDelayed(task: Runnable, delayMs: Long) = postAt(task, now + delayMs)
    override fun postAt(task: Runnable, uptimeMs: Long) { tasks += Task(uptimeMs, nextSeq++, task) }
    override fun cancel(task: Runnable) { tasks.removeAll { it.task === task } }

    /** Timers pending (a quiet timer armed, a Toast to take down). */
    val pending: Int get() = tasks.size

    fun advance(ms: Long) = advanceTo(now + ms)

    fun advanceTo(t: Long) {
        while (true) {
            val due = tasks.filter { it.at <= t }.minWithOrNull(compareBy<Task>({ it.at }, { it.seq })) ?: break
            tasks.remove(due)
            if (due.at > now) now = due.at
            due.task.run()
        }
        if (t > now) now = t
    }

    /** Runs [action] every [everyMs] from [startAt] to [until] (an app that keeps changing). */
    fun repeat(startAt: Long, everyMs: Long, until: Long, action: () -> Unit) {
        lateinit var tick: Runnable
        tick = Runnable {
            action()
            if (now + everyMs <= until) postAt(tick, now + everyMs)
        }
        postAt(tick, startAt)
    }
}

class FakeProbe : UiProbe {
    var activeWindow = -1
    var activePkg: String? = null
    /** When the window presented its last frame, read when asked: (now in ms) -> presented ns. */
    var framePresented: (() -> Long)? = null
    val settings = HashMap<String, String>()
    val cleared = ArrayList<Int>()

    override fun activeWindowId() = activeWindow
    override fun activePackage() = activePkg
    override fun clearFrameStats(window: Int) { cleared += window }
    override fun lastFramePresentedNanos(window: Int): Long = framePresented?.invoke() ?: 0
    override fun setting(ns: String, key: String): String? = settings["$ns/$key"]
}

/** Records what is injected. [onTouch] and [onKey] stand in for the app reacting. */
class FakeInjector(override val density: Float = 2f) : Injector {
    class Sent<T>(val event: T, val sync: Boolean)
    val touches = ArrayList<Sent<Touch>>()
    val keys = ArrayList<Sent<Key>>()
    var onTouch: (Touch) -> Unit = {}
    var onKey: (Key) -> Unit = {}

    override val path = "fake"
    override fun touch(t: Touch, sync: Boolean) { touches += Sent(t, sync); onTouch(t) }
    override fun key(k: Key, sync: Boolean) { keys += Sent(k, sync); onKey(k) }

    /** a-z and space have keys; anything else (an emoji) has none. */
    override fun keysFor(ch: Char): List<Key>? {
        val code = when (ch) {
            in 'a'..'z' -> KeyEvent.KEYCODE_A + (ch - 'a')
            ' ' -> KeyEvent.KEYCODE_SPACE
            else -> return null
        }
        return listOf(Key(KeyEvent.ACTION_DOWN, code), Key(KeyEvent.ACTION_UP, code))
    }
}

class FakeField(
    var value: String = "",
    override var editable: Boolean = true,
    override var showingHint: Boolean = false,
    override var selectionStart: Int = value.length,
    override var selectionEnd: Int = value.length,
    var acceptSelect: Boolean = true,
) : Field {
    override val text: CharSequence? get() = value
    val selects = ArrayList<Pair<Int, Int>>()

    override fun setText(text: CharSequence): Boolean { value = text.toString(); showingHint = false; return true }
    override fun select(start: Int, end: Int): Boolean {
        selects += start to end
        if (acceptSelect) { selectionStart = start; selectionEnd = end }
        return acceptSelect
    }
}

class FakeNode(
    override var className: CharSequence? = "android.view.View",
    override var text: CharSequence? = null,
    override var bounds: Box = Box(0, 0, 100, 100),
    override var visibleToUser: Boolean = true,
    var children: List<FakeNode> = emptyList(),
) : UiNode {
    override var packageName: CharSequence? = "com.app"
    override var windowId = 1
    override var contentDescription: CharSequence? = null
    override var viewId: String? = null
    override var hint: CharSequence? = null
    override var error: CharSequence? = null
    override var stateDescription: CharSequence? = null
    override var clickable = false
    override var enabled = true
    override var focused = false
    override var scrollable = false
    override var checked = false
    override var selected = false
    override var checkable = false
    override var password = false
    override var editable = false
    override var longClickable = false
    override var focusable = false
    override var showingHint = false
    override var canScrollForward = false
    override var canScrollBackward = false
    override var collection: IntArray? = null
    override var collectionItem: IntArray? = null
    override var drawingOrder = -1
    override val childCount: Int get() = children.size
    override fun child(i: Int): UiNode? = children.getOrNull(i)
}

class FakeWindow(
    override val id: Int,
    override val type: Int,
    var rootNode: FakeNode?,
    override val bounds: Box = Box(0, 0, 1080, 2400),
    override val layer: Int = 0,
    override val active: Boolean = false,
    override val focused: Boolean = false,
    override val title: CharSequence? = null,
) : UiWindow {
    override fun root(): UiNode? = rootNode
}

class FakeNodes : Nodes {
    var windowList: List<UiWindow> = emptyList()
    var active: UiNode? = null
    var field: Field? = null
    var cacheClears = 0

    override fun windows() = windowList
    override fun activeRoot() = active
    override fun clearCache() { cacheClears++ }
    override fun focusedInput() = field
}

class FakeDevice : Device {
    override var sdk = 36
    override var density = 2.0
    var size = intArrayOf(1080, 2400)
    var turned = 0
    val rotations = ArrayList<Rotation>()
    var foreground: String? = "com.app"
    var exits = 0
    val shells = ArrayList<String>()
    var shellResult = ShellResult(0, "ok\n", false)
    var shotRequest: Triple<Double, String, Int>? = null

    override fun displaySize() = size
    override fun rotation() = turned
    override fun rotate(to: Rotation) { rotations += to }
    override fun foregroundPackage() = foreground
    override fun screenshot(scale: Double, format: String, quality: Int): Shot {
        shotRequest = Triple(scale, format, quality)
        return Shot(540, 1200, format, ByteArray(10) { it.toByte() })
    }
    override fun shell(cmd: String, timeoutMs: Int): ShellResult { shells += cmd; return shellResult }
    override fun exitSoon() { exits++ }
}
