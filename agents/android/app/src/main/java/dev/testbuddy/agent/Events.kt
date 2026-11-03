package dev.testbuddy.agent

import android.app.UiAutomation
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent

/**
 * Turns accessibility events into "the UI changed" and "the UI settled" signals.
 *
 * Nothing here polls. Each UI-change accessibility event bumps [seq] and re-arms a quiet
 * timer; when the timer fires with no newer event the UI is settled and a "settled" event is
 * pushed to the host. Threads that wait for the UI ([awaitSettled], [awaitPackage]) block on
 * a monitor that events wake, with a timeout set to the earliest moment the answer could
 * change, so they use no CPU while they wait.
 *
 * Apps send content-change events at most every 100 ms (ViewConfiguration's
 * SendRecurringAccessibilityEventsInterval), so the quiet window must be longer than that.
 * Animations that send no accessibility events (a sheet sliding in) are caught by the active
 * window's frame stats, read once when the quiet timer fires: if it drew recently, the timer
 * is re-armed, for at most [FRAME_GRACE_MS] past the last event so an endless spinner cannot
 * hold the UI unsettled.
 */
class Events(private val ua: UiAutomation) {
    private val lock = Object()
    private val thread = HandlerThread("tb-events").apply { start() }
    private val handler = Handler(thread.looper)

    /** Every UI change, restless ones included: the host re-reads the screen on it. */
    @Volatile var seq = 0L; private set
    /** Changes that hold the UI unsettled (all but restless ones). */
    @Volatile var solid = 0L; private set
    /** The solid count when the UI last settled. */
    @Volatile var settledSolid = 0L; private set
    @Volatile var lastEventAt = 0L; private set   // uptime ms of the latest solid change
    @Volatile private var lastRestlessAt = 0L        // uptime ms of the latest restless change
    @Volatile var lastWindowChangeAt = 0L; private set
    @Volatile var foregroundPackage: String? = null; private set

    /** Where pushed events go: the connected host, if any. */
    @Volatile var sink: ((String) -> Unit)? = null

    var quietMs = DEFAULT_QUIET_MS
    private var lastPushedChangeAt = 0L
    private var frameWindow = -1

    private val quietTimer = Runnable { onQuiet() }

    // Restless sources: a view that has kept changing for RESTLESS_AFTER_MS with gaps under
    // RESTLESS_GAP_MS (a ticker, a stopwatch). Its changes still bump seq, but they do not
    // hold the UI unsettled, so actions on a screen that never stops moving still settle.
    // A source found restless stays so (until RESTLESS_MEMORY sources later): a ticker that
    // paused while its screen was hidden is not learned again when the screen comes back.
    // Accessibility node ids are not reused within a process, nor window ids across processes.
    // Only a view whose own text keeps changing is remembered: when several views change at
    // once the platform reports their common parent, and a parent remembered as restless would
    // hide the next screen coming into it.
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

    /** True when this event comes from a source that never stops changing. */
    private fun isRestless(e: AccessibilityEvent, now: Long): Boolean {
        val key = sourceKey(e) ?: return false
        if (knownRestless[key] == true) return true
        var src = sources[key]
        if (src == null || now - src.last > RESTLESS_GAP_MS) {
            src = Source(now, now)
            sources[key] = src
            if (sources.size > 512) sources.entries.removeIf { now - it.value.last > RESTLESS_GAP_MS }
        }
        src.last = now
        if (now - src.since < RESTLESS_AFTER_MS) return false
        if (e.eventType == AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED &&
            e.contentChangeTypes == AccessibilityEvent.CONTENT_CHANGE_TYPE_TEXT) knownRestless[key] = true
        return true
    }

    /**
     * The keyboard's package. Its suggestion bar redraws after every key for a while: not a
     * change in the app, so it does not hold the UI unsettled.
     */
    @Volatile private var imePackage: String? =
        setting("secure", "default_input_method")?.substringBefore('/')?.takeIf { it.isNotEmpty() && !it.contains(' ') }

    /**
     * Animations are off (animator duration scale 0, as `tb test` sets it): apps' own
     * animations end at once, so frames drawn while a restless view changes are its frames.
     * Read at start; the host updates it when it turns animations off or back on.
     */
    @Volatile var animationsOff: Boolean = setting("global", "animator_duration_scale")?.toFloatOrNull() == 0f

    private fun setting(ns: String, key: String): String? = try {
        val p = ProcessBuilder("settings", "get", ns, key).redirectErrorStream(true).start()
        val out = p.inputStream.bufferedReader().readText().trim()
        p.waitFor()
        out.takeIf { it.isNotEmpty() && it != "null" }
    } catch (_: Throwable) { null }

    init {
        ua.setOnAccessibilityEventListener { e -> onEvent(e) }
    }

    private fun onEvent(e: AccessibilityEvent) {
        val type = e.eventType
        val structural = type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED ||
            type == AccessibilityEvent.TYPE_WINDOWS_CHANGED
        // A view's click handler has run: the plainest sign that a tap was taken, and the only
        // one when its effect merges into a source that never stops changing (a tap on a screen
        // whose stopwatch the platform reports as the same changed subtree).
        val clicked = type == AccessibilityEvent.TYPE_VIEW_CLICKED || type == AccessibilityEvent.TYPE_VIEW_LONG_CLICKED
        val relevant = structural || clicked ||
            type == AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED ||
            type == AccessibilityEvent.TYPE_VIEW_SCROLLED ||
            type == AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED ||
            type == AccessibilityEvent.TYPE_VIEW_SELECTED
        if (!relevant) return
        val pkg = e.packageName?.toString()
        // The status bar redraws its clock and icons on its own: not a change in the app.
        if (pkg == "com.android.systemui" && !structural && foregroundPackage != "com.android.systemui") return

        val now = SystemClock.uptimeMillis()
        val ime = !structural && pkg != null && pkg == imePackage
        val restless = ime || (!structural && !clicked && type != AccessibilityEvent.TYPE_VIEW_SCROLLED && isRestless(e, now))
        if (debug) Log.d(TAG, "event ${AccessibilityEvent.eventTypeToString(type)} pkg=$pkg changes=${e.contentChangeTypes} restless=$restless src=${sourceKey(e)}")
        val s: Long
        synchronized(lock) {
            seq++
            s = seq
            if (!restless) {
                solid++
                lastEventAt = now
            } else {
                lastRestlessAt = now
            }
            if (structural) {
                lastWindowChangeAt = now
                if (type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED && pkg != null) foregroundPackage = pkg
            }
            lock.notifyAll()
        }
        // Coalesce change notifications: the host only needs a nudge, "settled" carries the final seq.
        if (now - lastPushedChangeAt >= 30) {
            lastPushedChangeAt = now
            push("{\"ev\":\"changed\",\"seq\":$s${if (pkg != null) ",\"pkg\":\"$pkg\"" else ""}}")
        }
        if (!restless) {
            handler.removeCallbacks(quietTimer)
            handler.postDelayed(quietTimer, quietMs.toLong())
        }
    }

    private fun onQuiet() {
        val now = SystemClock.uptimeMillis()
        val drewAt = lastFrameAt()
        // With animations off, the window drawing while something restless changes is that
        // thing drawing: holding for its frames would hold every action on a never-still screen
        // for FRAME_GRACE_MS. (With animations on, a sheet sliding in draws too: wait for it.)
        val restlessDrawing = animationsOff && now - lastRestlessAt < RESTLESS_DRAW_MS
        if (debug) Log.d(TAG, "quiet: drew ${now - drewAt} ms ago, last solid ${now - lastEventAt} ms ago, restless drawing $restlessDrawing")
        if (drewAt > 0 && now - drewAt < quietMs && now - lastEventAt < FRAME_GRACE_MS && !restlessDrawing) {
            handler.postDelayed(quietTimer, quietMs - (now - drewAt) + 1)
            return
        }
        val s: Long
        synchronized(lock) {
            s = seq
            settledSolid = solid
            lock.notifyAll()
        }
        push("{\"ev\":\"settled\",\"seq\":$s}")
    }

    private fun push(msg: String) {
        try { sink?.invoke(msg) } catch (_: Throwable) {}
    }

    /** Called before an action: frame stats then cover what the action makes the app draw. */
    fun beginAction() {
        try {
            val w = ua.rootInActiveWindow?.windowId ?: -1
            frameWindow = w
            if (w >= 0) ua.clearWindowContentFrameStats(w)
        } catch (_: Throwable) {}
    }

    /** Uptime ms of the active window's latest frame since [beginAction], or 0. */
    private fun lastFrameAt(): Long {
        val w = frameWindow
        if (w < 0) return 0
        return try {
            val stats = ua.getWindowContentFrameStats(w) ?: return 0
            val n = stats.frameCount
            if (n == 0) return 0
            val presented = stats.getFramePresentedTimeNano(n - 1)
            if (presented <= 0) return 0
            // Presented times are CLOCK_MONOTONIC, the same clock as uptime (in ms) plus deep sleep.
            val ageMs = (System.nanoTime() - presented) / 1_000_000
            SystemClock.uptimeMillis() - ageMs
        } catch (_: Throwable) { 0 }
    }

    enum class Settle { CHANGED, NO_CHANGE, TIMEOUT }

    /**
     * Waits for the UI to settle after an action taken when [solid] was [since]:
     * CHANGED once it changed and then went quiet, NO_CHANGE if nothing changed within
     * [noChangeMs], TIMEOUT if it kept changing for [timeoutMs]. Restless sources (a ticker)
     * neither count as a change nor keep it from settling.
     */
    /**
     * Waits for the UI to react to an action taken when the solid count was [since]: for a
     * solid change (up to [noChangeMs]), then for it to settle. With [reactOnly], the change
     * starting is enough: the host waits for the rest when it next needs a still screen.
     */
    fun awaitSettled(since: Long, noChangeMs: Int, timeoutMs: Int, reactOnly: Boolean = false): Settle {
        val start = SystemClock.uptimeMillis()
        val deadline = start + timeoutMs
        synchronized(lock) {
            while (true) {
                val now = SystemClock.uptimeMillis()
                if (solid == since) {
                    val giveUp = start + noChangeMs
                    if (now >= giveUp) return Settle.NO_CHANGE
                    lock.wait(minOf(giveUp, deadline) - now)
                    continue
                }
                if (reactOnly || settledSolid == solid) return Settle.CHANGED
                if (now >= deadline) return Settle.TIMEOUT
                lock.wait(maxOf(1L, deadline - now))
            }
        }
        @Suppress("UNREACHABLE_CODE")
        return Settle.TIMEOUT
    }

    /** Waits until no solid change is pending, up to timeoutMs. */
    fun awaitQuiet(timeoutMs: Int): Boolean {
        val deadline = SystemClock.uptimeMillis() + timeoutMs
        synchronized(lock) {
            while (settledSolid != solid) {
                val left = deadline - SystemClock.uptimeMillis()
                if (left <= 0) return false
                lock.wait(left)
            }
        }
        return true
    }

    /** Waits until [pkg] owns the active window. Wakes on window events only. */
    fun awaitPackage(pkg: String, timeoutMs: Int): Boolean {
        val deadline = SystemClock.uptimeMillis() + timeoutMs
        var checkedAt = -1L
        synchronized(lock) {
            while (true) {
                if (checkedAt != lastWindowChangeAt || checkedAt < 0) {
                    checkedAt = lastWindowChangeAt
                    val active = try { ua.rootInActiveWindow?.packageName?.toString() } catch (_: Throwable) { null }
                    if (active != null) foregroundPackage = active
                    if (active == pkg) return true
                }
                val left = deadline - SystemClock.uptimeMillis()
                if (left <= 0) return false
                lock.wait(left)
            }
        }
        @Suppress("UNREACHABLE_CODE")
        return false
    }

    companion object {
        const val DEFAULT_QUIET_MS = 120
        const val FRAME_GRACE_MS = 600
        const val RESTLESS_AFTER_MS = 500L
        const val RESTLESS_GAP_MS = 400L
        const val RESTLESS_MEMORY = 256
        const val RESTLESS_DRAW_MS = 250L
        /** `adb shell setprop log.tag.TestBuddyAgent DEBUG` logs every event and quiet check. */
        val debug: Boolean get() = Log.isLoggable(TAG, Log.DEBUG)
    }
}
