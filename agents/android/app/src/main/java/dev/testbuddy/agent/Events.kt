package dev.testbuddy.agent

import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent

class Events(
    private val probe: UiProbe,
    private val clock: Clock,
    private val background: (Runnable) -> Unit = { Thread(it, "tb-events-bg").apply { isDaemon = true }.start() },
) {
    private val lock = Object()

    @Volatile var seq = 0L; private set
    @Volatile var solid = 0L; private set
    @Volatile var clicks = 0L; private set
    @Volatile var solidSeq = 0L; private set
    @Volatile var settledSolid = 0L; private set
    @Volatile private var wakes = 0L
    @Volatile private var windowChanges = 0L
    @Volatile var lastEventAt = 0L; private set
    @Volatile private var lastRestlessAt = 0L
    @Volatile var lastWindowChangeAt = 0L; private set
    @Volatile var foregroundPackage: String? = null; private set
    @Volatile var openedWindow: OpenedWindow? = null; private set
    fun activeWindowId(): Int = try { probe.activeWindowId() } catch (_: Throwable) { -1 }
    val windows = WindowAges().also { w -> try { listedWindows().let { (ids, boxes) -> w.prime(ids, boxes) } } catch (_: Throwable) {} }

    private fun listedWindows(): Pair<IntArray?, Map<Int, Box>?> {
        val boxes = try { probe.windowBounds() } catch (_: Throwable) { null }
        return (boxes?.keys?.toIntArray() ?: try { probe.windowIds() } catch (_: Throwable) { null }) to boxes
    }

    @Volatile var sink: ((String) -> Unit)? = null

    var quietMs = DEFAULT_QUIET_MS
    private var frameWindow = -1

    private val quietTimer = Runnable { onQuiet() }

    private val pushLock = Object()
    private var lastPushedChangeAt = Long.MIN_VALUE / 2
    private var pushedSeq = 0L
    private var pendingPkg: String? = null
    private var flushArmed = false
    private val flushTimer = Runnable { flushChange() }

    private class Source(var since: Long, var last: Long)
    private val sources = HashMap<Long, Source>()
    private val knownRestless = object : LinkedHashMap<Long, Boolean>(64, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<Long, Boolean>) = size > RESTLESS_MEMORY
    }
    private val sourceIdMethod = try {
        AccessibilityEvent::class.java.getMethod("getSourceNodeId")
    } catch (_: Throwable) { null }

    private fun sourceKey(e: AccessibilityEvent): Long? {
        val id = try { sourceIdMethod?.invoke(e) as? Long } catch (_: Throwable) { null } ?: return null
        if (id == -1L || id == Long.MAX_VALUE) return null
        return (e.windowId.toLong() shl 48) xor id
    }

    private fun isRestless(e: UiEvent, now: Long): Boolean {
        val key = e.source ?: return false
        if (knownRestless[key] == true) return true
        var src = sources[key]
        if (src == null || now - src.last > RESTLESS_GAP_MS) {
            src = Source(now, now)
            sources[key] = src
            if (sources.size > 512) sources.entries.removeIf { now - it.value.last > RESTLESS_GAP_MS }
        }
        src.last = now
        if (now - src.since < RESTLESS_AFTER_MS) return false
        if (e.type == AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED &&
            e.contentChangeTypes == AccessibilityEvent.CONTENT_CHANGE_TYPE_TEXT) knownRestless[key] = true
        return true
    }

    @Volatile private var imePackage: String? = readImePackage()

    private fun readImePackage(): String? =
        setting("secure", "default_input_method")?.substringBefore('/')?.takeIf { it.isNotEmpty() && !it.contains(' ') }

    private val statePackages = HashSet<String>()
    private val refreshIme = Runnable {
        val ime = readImePackage()
        imePackage = ime
        synchronized(lock) { if (ime != null && openedPackage == ime) openedWindow = null }
    }
    private var openedPackage: String? = null

    private fun noteOpened(e: UiEvent, pkg: String?, now: Long) {
        if (pkg == null || e.windowId < 0 || e.contentChangeTypes != 0) return
        if (pkg == "com.android.systemui" || pkg == "android" || pkg == imePackage) return
        openedWindow = OpenedWindow(e.windowId, now)
        openedPackage = pkg
        if (statePackages.size < 64 && statePackages.add(pkg)) background(refreshIme)
    }

    @Volatile var animationsOff: Boolean = setting("global", "animator_duration_scale")?.toFloatOrNull() == 0f

    private fun setting(ns: String, key: String): String? = try { probe.setting(ns, key) } catch (_: Throwable) { null }

    fun onEvent(e: UiEvent) {
        val type = e.type
        if (type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED && e.contentChangeTypes == 0 && e.windowId >= 0)
            windows.opened(e.windowId, e.pkg, e.className, if (e.eventTime > 0) e.eventTime else clock.uptimeMillis())
        if (type == AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED &&
            e.className?.toString()?.startsWith("android.widget.Toast") == true) {
            onToast(e)
            return
        }
        val structural = type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED ||
            type == AccessibilityEvent.TYPE_WINDOWS_CHANGED
        if (type == AccessibilityEvent.TYPE_WINDOWS_CHANGED) {
            val (ids, boxes) = listedWindows()
            windows.update(ids, if (e.eventTime > 0) e.eventTime else clock.uptimeMillis(), boxes)
        }
        val clicked = type == AccessibilityEvent.TYPE_VIEW_CLICKED || type == AccessibilityEvent.TYPE_VIEW_LONG_CLICKED
        val relevant = structural || clicked ||
            type == AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED ||
            type == AccessibilityEvent.TYPE_VIEW_SCROLLED ||
            type == AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED ||
            type == AccessibilityEvent.TYPE_VIEW_SELECTED
        noteAppEvent(e)
        if (!relevant) return
        val pkg = e.pkg
        if (pkg == "com.android.systemui" && !structural && foregroundPackage != "com.android.systemui") return

        val now = clock.uptimeMillis()
        val ime = !structural && pkg != null && pkg == imePackage
        val restless = ime || (!structural && !clicked && type != AccessibilityEvent.TYPE_VIEW_SCROLLED && isRestless(e, now))
        if (debug) Log.d(TAG, "event ${AccessibilityEvent.eventTypeToString(type)} pkg=$pkg changes=${e.contentChangeTypes} restless=$restless src=${e.source}")
        synchronized(lock) {
            seq++
            if (clicked) clicks++
            if (!restless) {
                solid++
                solidSeq = seq
                lastEventAt = now
            } else {
                lastRestlessAt = now
            }
            if (structural) {
                lastWindowChangeAt = now
                windowChanges++
                if (type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED && pkg != null) foregroundPackage = pkg
                if (type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) noteOpened(e, pkg, now)
            }
            wake()
        }
        pushChanged(pkg, now, force = false)
        if (!restless) {
            clock.cancel(quietTimer)
            clock.postDelayed(quietTimer, quietMs.toLong())
        }
    }

    private fun pushChanged(pkg: String?, now: Long, force: Boolean) {
        synchronized(pushLock) {
            if (seq <= pushedSeq) return
            if (!force && now - lastPushedChangeAt < COALESCE_MS) {
                pendingPkg = pkg ?: pendingPkg
                if (!flushArmed) {
                    flushArmed = true
                    clock.postAt(flushTimer, lastPushedChangeAt + COALESCE_MS)
                }
                return
            }
            sendChanged(pkg, now)
        }
    }

    private fun flushChange() {
        synchronized(pushLock) {
            flushArmed = false
            if (seq <= pushedSeq) return
            sendChanged(pendingPkg, clock.uptimeMillis())
        }
    }

    private fun sendChanged(pkg: String?, now: Long) {
        val s = seq
        lastPushedChangeAt = now
        pushedSeq = s
        pendingPkg = null
        push("{\"ev\":\"changed\",\"seq\":$s${if (pkg != null) ",\"pkg\":\"$pkg\"" else ""}}")
    }

    private fun onToast(e: UiEvent) {
        val texts = e.texts.filter { it.isNotBlank() }
        if (texts.isEmpty()) return
        val until = clock.uptimeMillis() + Toasts.SHOWN_MS
        Toasts.texts = texts
        Toasts.until = until
        changedNow(e.pkg)
        clock.postAt({ if (Toasts.until == until) changedNow(null) }, until + 1)
    }

    private fun changedNow(pkg: String?) {
        val now = clock.uptimeMillis()
        synchronized(lock) {
            seq++
            solid++
            solidSeq = seq
            lastEventAt = now
            wake()
        }
        pushChanged(pkg, now, force = true)
        clock.cancel(quietTimer)
        clock.postDelayed(quietTimer, quietMs.toLong())
    }

    private fun onQuiet() {
        val now = clock.uptimeMillis()
        val drewAt = lastFrameAt()
        val restlessDrawing = animationsOff && now - lastRestlessAt < RESTLESS_DRAW_MS
        if (debug) Log.d(TAG, "quiet: drew ${now - drewAt} ms ago, last solid ${now - lastEventAt} ms ago, restless drawing $restlessDrawing")
        if (drewAt > 0 && now - drewAt < quietMs && now - lastEventAt < FRAME_GRACE_MS && !restlessDrawing) {
            clock.postDelayed(quietTimer, quietMs - (now - drewAt) + 1)
            return
        }
        var wait = 0L
        synchronized(pushLock) {
            var s = -1L
            synchronized(lock) {
                val sinceChange = clock.uptimeMillis() - lastEventAt
                if (sinceChange < quietMs) {
                    wait = quietMs - sinceChange
                } else if (settledSolid != solid) {
                    s = seq
                    settledSolid = solid
                    wake()
                }
            }
            if (s >= 0) {
                if (s > pushedSeq) pushedSeq = s
                push("{\"ev\":\"settled\",\"seq\":$s}")
            }
        }
        if (wait > 0) clock.postDelayed(quietTimer, wait)
    }

    private fun push(msg: String) {
        try { sink?.invoke(msg) } catch (_: Throwable) {}
    }

    fun beginAction() {
        try {
            val w = probe.activeWindowId()
            frameWindow = w
            if (w >= 0) probe.clearFrameStats(w)
        } catch (_: Throwable) {}
    }

    private val appEventAt = java.util.concurrent.ConcurrentHashMap<String, Long>()

    private fun noteAppEvent(e: UiEvent) {
        val pkg = e.pkg ?: return
        if (pkg == imePackage || pkg == "com.android.systemui" || pkg == "android") return
        when (e.type) {
            AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED,
            AccessibilityEvent.TYPE_VIEW_TEXT_SELECTION_CHANGED, AccessibilityEvent.TYPE_VIEW_FOCUSED,
            AccessibilityEvent.TYPE_VIEW_SCROLLED, AccessibilityEvent.TYPE_VIEW_SELECTED,
            AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED -> appEventAt[pkg] = clock.uptimeMillis()
        }
    }

    fun lastAppEventAt(pkg: String?): Long =
        if (pkg != null) appEventAt[pkg] ?: 0 else appEventAt.values.maxOrNull() ?: 0

    fun lastFrameAt(): Long {
        val w = frameWindow
        if (w < 0) return 0
        return try {
            val presented = probe.lastFramePresentedNanos(w)
            if (presented <= 0) return 0
            val ageMs = (clock.nanoTime() - presented) / 1_000_000
            clock.uptimeMillis() - ageMs
        } catch (_: Throwable) { 0 }
    }

    enum class Settle { CHANGED, NO_CHANGE, TIMEOUT }

    fun awaitSettled(since: Long, noChangeMs: Int, timeoutMs: Int, reactOnly: Boolean = false): Settle {
        val start = clock.uptimeMillis()
        val deadline = start + timeoutMs
        synchronized(lock) {
            while (true) {
                val now = clock.uptimeMillis()
                if (solidSeq <= since) {
                    val giveUp = minOf(start + noChangeMs, deadline)
                    if (now >= giveUp) return Settle.NO_CHANGE
                    clock.await(lock, giveUp - now)
                    continue
                }
                if (reactOnly || settledSolid == solid) return Settle.CHANGED
                if (now >= deadline) return Settle.TIMEOUT
                clock.await(lock, maxOf(1L, deadline - now))
            }
        }
        @Suppress("UNREACHABLE_CODE")
        return Settle.TIMEOUT
    }

    fun awaitQuiet(timeoutMs: Int): Boolean {
        val deadline = clock.uptimeMillis() + timeoutMs
        synchronized(lock) {
            while (settledSolid != solid) {
                val left = deadline - clock.uptimeMillis()
                if (left <= 0) return false
                clock.await(lock, left)
            }
        }
        return true
    }

    private fun wake() {
        wakes++
        lock.notifyAll()
    }

    fun awaitCondition(timeoutMs: Int, condition: () -> Boolean): Boolean {
        val deadline = clock.uptimeMillis() + timeoutMs.coerceAtLeast(0)
        while (true) {
            val seen = wakes
            if (condition()) return true
            val left = deadline - clock.uptimeMillis()
            if (left <= 0) return false
            synchronized(lock) {
                if (wakes == seen) clock.await(lock, left)
            }
        }
    }

    fun awaitPackage(pkg: String, timeoutMs: Int): Boolean {
        val deadline = clock.uptimeMillis() + timeoutMs
        while (true) {
            val seen = windowChanges
            val active = try { probe.activePackage() } catch (_: Throwable) { null }
            if (active != null) synchronized(lock) { if (windowChanges == seen) foregroundPackage = active }
            if (active == pkg) return true
            synchronized(lock) {
                while (true) {
                    val left = deadline - clock.uptimeMillis()
                    if (left <= 0) return false
                    if (windowChanges != seen) break
                    clock.await(lock, left)
                }
            }
        }
    }

    companion object {
        const val DEFAULT_QUIET_MS = 120
        const val COALESCE_MS = 30L
        const val FRAME_GRACE_MS = 600
        const val RESTLESS_AFTER_MS = 500L
        const val RESTLESS_GAP_MS = 400L
        const val RESTLESS_MEMORY = 256
        const val RESTLESS_DRAW_MS = 250L
        val debug: Boolean get() = Log.isLoggable(TAG, Log.DEBUG)
    }
}

object Toasts {
    const val SHOWN_MS = 3_500L
    @Volatile var texts: List<String> = emptyList()
    @Volatile var until = 0L

    fun showing(now: Long = SystemClock.uptimeMillis()): List<String> = if (now < until) texts else emptyList()
}

class OpenedWindow(val id: Int, val at: Long)
