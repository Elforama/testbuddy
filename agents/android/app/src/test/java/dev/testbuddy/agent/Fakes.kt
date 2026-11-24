package dev.testbuddy.agent

import android.view.KeyEvent

class FakeClock(var now: Long = 0) : Clock {
    var oversleep: Long = 0
    private class Task(val at: Long, val seq: Int, val task: Runnable)
    private val tasks = ArrayList<Task>()
    private var nextSeq = 0

    override fun uptimeMillis(): Long = now
    override fun nanoTime(): Long = now * 1_000_000
    override fun sleep(ms: Long) = advance(ms + oversleep)

    override fun await(lock: Object, ms: Long) {
        require(ms > 0) { "lock.wait($ms): ${if (ms == 0L) "waits forever" else "negative timeout"}" }
        val next = tasks.minOfOrNull { it.at } ?: Long.MAX_VALUE
        advanceTo(minOf(now + ms, maxOf(next, now)))
    }

    override fun postDelayed(task: Runnable, delayMs: Long) = postAt(task, now + delayMs)
    override fun postAt(task: Runnable, uptimeMs: Long) { tasks += Task(uptimeMs, nextSeq++, task) }
    override fun cancel(task: Runnable) { tasks.removeAll { it.task === task } }

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
    var framePresented: (() -> Long)? = null
    val settings = HashMap<String, String>()
    val cleared = ArrayList<Int>()

    override fun activeWindowId() = activeWindow
    override fun activePackage() = activePkg
    override fun clearFrameStats(window: Int) { cleared += window }
    override fun lastFramePresentedNanos(window: Int): Long = framePresented?.invoke() ?: 0
    override fun setting(ns: String, key: String): String? = settings["$ns/$key"]
    var windows: IntArray? = intArrayOf()
    override fun windowIds(): IntArray? = windows
    var boxes: Map<Int, Box> = emptyMap()
    override fun windowBounds(): Map<Int, Box>? = windows?.associateWith { boxes[it] ?: Box(0, 0, 0, 0) }
}

class FakeInjector(override val density: Float = 2f) : Injector {
    var onPrepareSwipe: () -> Unit = {}
    override fun prepareSwipe() = onPrepareSwipe()
    class Sent<T>(val event: T, val sync: Boolean)
    val touches = ArrayList<Sent<Touch>>()
    val keys = ArrayList<Sent<Key>>()
    var onTouch: (Touch) -> Unit = {}
    var onSyncTouch: (Touch) -> Unit = {}
    var onKey: (Key) -> Unit = {}

    override val path = "fake"
    var accept: (Touch, Boolean) -> Boolean = { _, _ -> true }
    override fun touch(t: Touch, sync: Boolean): Boolean {
        touches += Sent(t, sync)
        onTouch(t)
        if (sync) onSyncTouch(t)
        return accept(t, sync)
    }
    override fun key(k: Key, sync: Boolean) { keys += Sent(k, sync); onKey(k) }

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
    override var inputType: Int = 0,
    override var packageName: CharSequence? = null,
    override var maxLength: Int = -1,
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

    var appBounds: Box? = null
    var appShown: Boolean? = null
    var gone = false
    var refreshes = 0
    var onRefresh: () -> Unit = {}
    override fun refresh(): Boolean {
        refreshes++
        onRefresh()
        if (gone) return false
        appBounds?.let { bounds = it }
        appShown?.let { visibleToUser = it }
        return true
    }
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
    var activeError: Throwable? = null
    override fun activeRoot(prefetchChildren: Boolean): UiNode? {
        activeRootReads++; prefetchRequests += prefetchChildren
        activeError?.let { throw it }
        return active
    }
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
    var answer: ((String) -> ShellResult)? = null
    var shotRequest: Triple<Double, String, Int>? = null

    override fun displaySize() = size
    override fun rotation() = turned
    override fun rotate(to: Rotation) { rotations += to }
    override fun foregroundPackage() = foreground
    val activities = HashSet<String>()
    override fun isActivity(pkg: String, cls: String) = "$pkg/$cls" in activities
    override fun screenshot(scale: Double, format: String, quality: Int): Shot {
        shotRequest = Triple(scale, format, quality)
        return Shot(540, 1200, format, ByteArray(10) { it.toByte() })
    }
    override fun shell(cmd: String, timeoutMs: Int): ShellResult { shells += cmd; return answer?.invoke(cmd) ?: shellResult }
    val dumps = ArrayList<String>()
    var dumpAnswer: ((String) -> ShellResult?)? = null
    override fun dump(service: String, args: List<String>, timeoutMs: Int): ShellResult? {
        val call = (listOf(service) + args).joinToString(" ")
        dumps += call
        return dumpAnswer?.invoke(call)
    }
    override fun exitSoon() { exits++ }
}

class RewritingField(
    private val clock: FakeClock,
    private val frameMs: Long = 16,
    private val transform: (String) -> String = { it.uppercase() },
    private val lagging: (Char) -> Boolean = { false },
    private val takesDel: Boolean = true,
    inputType: Int = 0,
    private val showAfter: (Char?) -> Long = { frameMs },
    private val drops: (Char, Int) -> Boolean = { _, _ -> false },
) {
    val field = FakeField(nativePlainText = true, selectionStart = 0, selectionEnd = 0, inputType = inputType)
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
