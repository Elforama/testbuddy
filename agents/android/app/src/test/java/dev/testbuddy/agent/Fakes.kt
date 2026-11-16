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
    /** How much later than asked a sleep returns (a busy device wakes the sleeping thread late). */
    var oversleep: Long = 0
    private class Task(val at: Long, val seq: Int, val task: Runnable)
    private val tasks = ArrayList<Task>()
    private var nextSeq = 0

    override fun uptimeMillis(): Long = now
    override fun nanoTime(): Long = now * 1_000_000
    override fun sleep(ms: Long) = advance(ms + oversleep)

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
    /** The windows on screen (null: they cannot be read). */
    var windows: IntArray? = intArrayOf()
    override fun windowIds(): IntArray? = windows
}

/** Records what is injected. [onTouch] and [onKey] stand in for the app reacting. */
class FakeInjector(override val density: Float = 2f) : Injector {
    var onPrepareSwipe: () -> Unit = {}
    override fun prepareSwipe() = onPrepareSwipe()
    class Sent<T>(val event: T, val sync: Boolean)
    val touches = ArrayList<Sent<Touch>>()
    val keys = ArrayList<Sent<Key>>()
    var onTouch: (Touch) -> Unit = {}
    /** A touch injected waiting for the app to finish handling it (a busy app returns late). */
    var onSyncTouch: (Touch) -> Unit = {}
    var onKey: (Key) -> Unit = {}

    override val path = "fake"
    /** Whether the platform takes a touch (false: reported not injected, issue #204). */
    var accept: (Touch, Boolean) -> Boolean = { _, _ -> true }
    override fun touch(t: Touch, sync: Boolean): Boolean {
        touches += Sent(t, sync)
        onTouch(t)
        if (sync) onSyncTouch(t)
        return accept(t, sync)
    }
    override fun key(k: Key, sync: Boolean) { keys += Sent(k, sync); onKey(k) }

    /** a-z, 0-9 and space have keys; anything else (an emoji) has none. */
    override fun keysFor(ch: Char): List<Key>? {
        val code = when (ch) {
            in 'a'..'z' -> KeyEvent.KEYCODE_A + (ch - 'a')
            in '0'..'9' -> KeyEvent.KEYCODE_0 + (ch - '0')
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
    override val nativePlainText: Boolean = false,
    /** The node's input type (InputType); 0: the node does not say (issue #299). */
    override var inputType: Int = 0,
    override var packageName: CharSequence? = null,
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
    override var windowType = -1
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
    var activeRootReads = 0
    var windowReads = 0
    var onClearCache: () -> Unit = {}

    var prefetchRequests = ArrayList<Boolean>()
    override fun windows(prefetchChildren: Boolean): List<UiWindow> { windowReads++; prefetchRequests += prefetchChildren; return windowList }
    override fun activeRoot(prefetchChildren: Boolean): UiNode? { activeRootReads++; prefetchRequests += prefetchChildren; return active }
    override fun clearCache() { cacheClears++; onClearCache() }
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
    /** Answers by command when set, instead of [shellResult]. */
    var answer: ((String) -> ShellResult)? = null
    var shotRequest: Triple<Double, String, Int>? = null

    override fun displaySize() = size
    override fun rotation() = turned
    override fun rotate(to: Rotation) { rotations += to }
    override fun foregroundPackage() = foreground
    override fun screenshot(scale: Double, format: String, quality: Int): Shot {
        shotRequest = Triple(scale, format, quality)
        return Shot(540, 1200, format, ByteArray(10) { it.toByte() })
    }
    override fun shell(cmd: String, timeoutMs: Int): ShellResult { shells += cmd; return answer?.invoke(cmd) ?: shellResult }
    /** In-process dumps asked for ("service arg..."), and their answers by that string; null: cannot. */
    val dumps = ArrayList<String>()
    var dumpAnswer: ((String) -> ShellResult?)? = null
    override fun dump(service: String, args: List<String>, timeoutMs: Int): ShellResult? {
        val call = (listOf(service) + args).joinToString(" ")
        dumps += call
        return dumpAnswer?.invoke(call)
    }
    override fun exitSoon() { exits++ }
}

/**
 * Issue #279: a controlled text field whose app rewrites every value it is given, as a Compose
 * TextField with `onValueChange = { value = it.uppercase() }`. Install it with [attach].
 *
 * A key edits the value the field last restarted its input with, not the latest one: each new
 * value restarts the field's input, which is through a frame ([frameMs], on the clock) after
 * the key, when the field also shows the new value. A key that comes before that frame (keys
 * sent together, with no wait between them) edits the value from before the key ahead of it,
 * and so overwrites that key: the field ends up with a subsequence of what was typed.
 *
 * A key for which [lagging] is true shows in the field a frame later as any other, but its
 * restart never comes: the next key edits the value from before it, whatever the pace (a
 * field that loses that key for good).
 */
class RewritingField(
    private val clock: FakeClock,
    private val frameMs: Long = 16,
    private val transform: (String) -> String = { it.uppercase() },
    private val lagging: (Char) -> Boolean = { false },
    /** False: a DEL changes nothing (issue #297 review). */
    private val takesDel: Boolean = true,
    /** The node's input type (issue #299: a phone field). */
    inputType: Int = 0,
    /** How long after a key the field shows it and restarts (issue #299: an app under load); [frameMs] by default. */
    private val showAfter: (Char?) -> Long = { frameMs },
    /**
     * Issue #299: the app drops a key outright, showing nothing of it, when this says so; its
     * argument is how many times that character's key has come (1 the first time).
     */
    private val drops: (Char, Int) -> Boolean = { _, _ -> false },
) {
    val field = FakeField(nativePlainText = true, selectionStart = 0, selectionEnd = 0, inputType = inputType)
    /** The value a key edits: the one the field last restarted its input with. */
    private var base = ""
    private val seen = HashMap<Char, Int>()

    fun attach(injector: FakeInjector) { injector.onKey = ::onKey }

    private fun onKey(k: Key) {
        if (k.action != KeyEvent.ACTION_DOWN) return
        val ch = when (k.code) {
            in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> 'a' + (k.code - KeyEvent.KEYCODE_A)
            in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 -> '0' + (k.code - KeyEvent.KEYCODE_0)
            KeyEvent.KEYCODE_SPACE -> ' '
            KeyEvent.KEYCODE_DEL -> if (takesDel) null else return
            else -> return
        }
        if (ch != null) {
            val n = (seen[ch] ?: 0) + 1
            seen[ch] = n
            if (drops(ch, n)) return
        }
        val value = transform(if (ch == null) base.dropLast(1) else base + ch)
        val restarts = ch == null || !lagging(ch)
        clock.postDelayed({
            field.value = value
            field.selectionStart = value.length
            field.selectionEnd = value.length
            if (restarts) base = value
        }, showAfter(ch))
    }
}

/**
 * Issue #297: a field that takes every key and loses none, but shows each one late, as a
 * Flutter field whose engine renders behind the accessibility tree: the app handles the keys
 * in order, each taking [lagFrames] frames (in turn, 3 to 5), and the field shows the text up
 * to a key once that key is handled. Injecting a key takes [injectMs] on the clock. Install it
 * with [attach].
 */
class LaggingField(
    private val clock: FakeClock,
    private val lagFrames: List<Int> = listOf(4, 3, 5, 3, 4),
    private val injectMs: Long = 4,
) {
    val field = FakeField(nativePlainText = true, selectionStart = 0, selectionEnd = 0)
    private var handled = ""
    private var busyUntil = 0L
    private var keys = 0

    fun attach(injector: FakeInjector) { injector.onKey = ::onKey }

    private fun onKey(k: Key) {
        clock.advance(injectMs)
        if (k.action != KeyEvent.ACTION_DOWN) return
        val ch = when (k.code) {
            in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> 'a' + (k.code - KeyEvent.KEYCODE_A)
            KeyEvent.KEYCODE_SPACE -> ' '
            KeyEvent.KEYCODE_DEL -> null
            else -> return
        }
        handled = if (ch == null) handled.dropLast(1) else handled + ch
        val shows = handled
        busyUntil = maxOf(busyUntil, clock.now) + lagFrames[keys++ % lagFrames.size] * Input.FRAME_MS
        clock.postAt({
            field.value = shows
            field.selectionStart = shows.length
            field.selectionEnd = shows.length
        }, busyUntil)
    }
}

/**
 * Issue #289: a plain field erased at its caret: keys go in at the caret, a DEL deletes the
 * grapheme before it (an emoji with its skin tone whole), text set replaces it all and puts
 * the caret at its end. [filter] keeps the characters the field takes; [lose] is lost once,
 * from a key or a text set.
 *
 * With [reportsCaretAtEnd], accessibility reports the selection at the end of the
 * text wherever the real caret is, as a Compose field did after a tap that left its caret
 * mid-text. [takesSelect] (ACTION_SET_SELECTION), [takesEndKey], [takesDel] and [takesSetText]
 * false make a field that ignores that way of editing it. With [later], a DEL shows in the
 * field when [later] runs it (a frame after the key, as in Compose). [app] is the app's own
 * rewrite of every value the field is given (a mask, a formatter, a prefix put back). With
 * [selectLater], a selection the field accepts moves its caret only when [selectLater] runs it
 * (a Compose field applies ACTION_SET_SELECTION a frame later, issue #289 on the device).
 */
class EraseField(
    var value: String = "",
    var caret: Int = value.length,
    var lose: Char? = null,
    val filter: (Char) -> Boolean = { true },
    var reportsCaretAtEnd: Boolean = false,
    var takesSelect: Boolean = true,
    var takesEndKey: Boolean = true,
    var takesDel: Boolean = true,
    var takesSetText: Boolean = true,
    var later: ((() -> Unit) -> Unit)? = null,
    val app: (String) -> String = { it },
    var selectLater: ((() -> Unit) -> Unit)? = null,
) : Field {
    override val editable = true
    override val showingHint = false
    override val nativePlainText = true
    override val text: CharSequence get() = value
    override val selectionStart get() = if (reportsCaretAtEnd) value.length else caret
    override val selectionEnd get() = if (reportsCaretAtEnd) value.length else caret
    /** The texts set on it (ACTION_SET_TEXT). */
    val sets = ArrayList<String>()

    override fun setText(text: CharSequence): Boolean {
        if (!takesSetText) return false
        sets += text.toString()
        var s = text.toString()
        lose?.let { c -> if (s.indexOf(c) >= 0) { s = s.replaceFirst(c.toString(), ""); lose = null } }
        value = app(s.filter(filter))
        caret = value.length
        return true
    }

    override fun select(start: Int, end: Int): Boolean {
        if (!takesSelect) return false
        val move = { caret = end.coerceIn(0, value.length) }
        selectLater?.invoke(move) ?: move()
        return true
    }

    fun key(k: Key) {
        if (k.action != KeyEvent.ACTION_DOWN) return
        if (k.code == KeyEvent.KEYCODE_MOVE_END) { if (takesEndKey) caret = value.length; return }
        if (k.code == KeyEvent.KEYCODE_DEL) {
            if (!takesDel) return
            val del = {
                if (caret > 0) {
                    var i = value.offsetByCodePoints(caret, -1)
                    if (value.codePointAt(i) in 0x1F3FB..0x1F3FF && i > 0) i = value.offsetByCodePoints(i, -1)
                    value = value.removeRange(i, caret)
                    caret = i
                    val shown = app(value)
                    if (shown != value) { value = shown; caret = value.length }
                }
            }
            later?.invoke(del) ?: del()
            return
        }
        val ch = when (k.code) {
            in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> 'a' + (k.code - KeyEvent.KEYCODE_A)
            KeyEvent.KEYCODE_SPACE -> ' '
            else -> return
        }
        if (ch == lose) { lose = null; return }
        if (!filter(ch)) return
        value = value.substring(0, caret) + ch + value.substring(caret)
        caret++
    }
}
