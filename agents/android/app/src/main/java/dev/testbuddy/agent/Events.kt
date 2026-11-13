package dev.testbuddy.agent

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
 *
 * The device side ([UiProbe], and [Clock]'s timers) is behind interfaces: on the device the
 * events come from UiAutomation (AndroidPlatform.kt), in unit tests from the test.
 */
class Events(
    private val probe: UiProbe,
    private val clock: Clock,
    /** Runs slow work (reading a setting starts a process) off the event and timer threads. */
    private val background: (Runnable) -> Unit = { Thread(it, "tb-events-bg").apply { isDaemon = true }.start() },
) {
    private val lock = Object()

    /** Every UI change, restless ones included: the host re-reads the screen on it. */
    @Volatile var seq = 0L; private set
    /** Changes that hold the UI unsettled (all but restless ones). */
    @Volatile var solid = 0L; private set
    /** View clicks reported (a click handler ran): tells whether a tap reached a view. */
    @Volatile var clicks = 0L; private set
    /** The solid count when the UI last settled. */
    @Volatile var settledSolid = 0L; private set
    @Volatile var lastEventAt = 0L; private set   // uptime ms of the latest solid change
    @Volatile private var lastRestlessAt = 0L        // uptime ms of the latest restless change
    @Volatile var lastWindowChangeAt = 0L; private set
    @Volatile var foregroundPackage: String? = null; private set
    /** The window an app's latest window-state event came from: the screen it opened ([Tree], issue #211). */
    @Volatile var openedWindow: OpenedWindow? = null; private set
    /** When each window on screen was first seen (issue #204). */
    val windows = WindowAges().also { w -> try { w.prime(probe.windowIds()) } catch (_: Throwable) {} }

    /** Where pushed events go: the connected host, if any. */
    @Volatile var sink: ((String) -> Unit)? = null

    var quietMs = DEFAULT_QUIET_MS
    private var frameWindow = -1

    private val quietTimer = Runnable { onQuiet() }

    // Pushing "changed": at most one per COALESCE_MS, and the last change of a burst pushed at
    // the end of its window (issue #87), so the host's count does not lag on a screen that does
    // not settle. Pushes come from the event thread and the timer thread: each reads the count
    // and hands its message to [sink] under [pushLock], so the counts pushed, "settled" ones
    // included, never go backwards. Lock order: [pushLock], then [lock]; nothing takes
    // [pushLock] while holding [lock]. The sink only queues (Outbox.kt): no I/O under the lock.
    private val pushLock = Object()
    private var lastPushedChangeAt = Long.MIN_VALUE / 2
    /** The highest count pushed, in "changed" or "settled". */
    private var pushedSeq = 0L
    private var pendingPkg: String? = null
    private var flushArmed = false
    private val flushTimer = Runnable { flushChange() }

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

    /**
     * The keyboard's package. Its suggestion bar redraws after every key for a while: not a
     * change in the app, so it does not hold the UI unsettled.
     */
    @Volatile private var imePackage: String? = readImePackage()

    private fun readImePackage(): String? =
        setting("secure", "default_input_method")?.substringBefore('/')?.takeIf { it.isNotEmpty() && !it.contains(' ') }

    /** Packages whose window-state events have been seen: one not seen before may be a new keyboard. */
    private val statePackages = HashSet<String>()
    private val refreshIme = Runnable {
        val ime = readImePackage()
        imePackage = ime
        synchronized(lock) { if (ime != null && openedPackage == ime) openedWindow = null }
    }
    private var openedPackage: String? = null

    /**
     * An app's window opened (issue #211): only a window-state event that is not a pane's change
     * (contentChangeTypes 0), and not from the status bar, the system or the keyboard. A
     * package not seen before may be a keyboard switched to: the keyboard's package is read again,
     * in the background. Called with [lock] held.
     */
    private fun noteOpened(e: UiEvent, pkg: String?, now: Long) {
        if (pkg == null || e.windowId < 0 || e.contentChangeTypes != 0) return
        if (pkg == "com.android.systemui" || pkg == "android" || pkg == imePackage) return
        openedWindow = OpenedWindow(e.windowId, now)
        openedPackage = pkg
        if (statePackages.size < 64 && statePackages.add(pkg)) background(refreshIme)
    }

    /**
     * Animations are off (animator duration scale 0, as `tb test` sets it): apps' own
     * animations end at once, so frames drawn while a restless view changes are its frames.
     * Read at start; the host updates it when it turns animations off or back on.
     */
    @Volatile var animationsOff: Boolean = setting("global", "animator_duration_scale")?.toFloatOrNull() == 0f

    private fun setting(ns: String, key: String): String? = try { probe.setting(ns, key) } catch (_: Throwable) { null }

    /** One accessibility event (on the device: UiAutomation's listener thread). */
    fun onEvent(e: UiEvent) {
        val type = e.type
        if (type == AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED &&
            e.className?.toString()?.startsWith("android.widget.Toast") == true) {
            onToast(e)
            return
        }
        val structural = type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED ||
            type == AccessibilityEvent.TYPE_WINDOWS_CHANGED
        if (type == AccessibilityEvent.TYPE_WINDOWS_CHANGED) {
            // A window added starts its age at the event's time, not when it reached us.
            windows.update(try { probe.windowIds() } catch (_: Throwable) { null },
                if (e.eventTime > 0) e.eventTime else clock.uptimeMillis())
        }
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
        val pkg = e.pkg
        // The status bar redraws its clock and icons on its own: not a change in the app.
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
                lastEventAt = now
            } else {
                lastRestlessAt = now
            }
            if (structural) {
                lastWindowChangeAt = now
                if (type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED && pkg != null) foregroundPackage = pkg
                if (type == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) noteOpened(e, pkg, now)
            }
            lock.notifyAll()
        }
        // Coalesce change notifications: the host only needs a nudge; a burst's last change is
        // pushed when its window ends (flushChange).
        pushChanged(pkg, now, force = false)
        if (!restless) {
            clock.cancel(quietTimer)
            clock.postDelayed(quietTimer, quietMs.toLong())
        }
    }

    /**
     * Pushes "changed" with the latest count, unless one was pushed less than [COALESCE_MS] ago:
     * then the push waits for the end of that window ([flushChange], issue #87). [force] pushes
     * at once (a Toast).
     */
    private fun pushChanged(pkg: String?, now: Long, force: Boolean) {
        synchronized(pushLock) {
            if (seq <= pushedSeq) return // a later push already carried it
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

    /** The end of a coalescing window: pushes the changes it held back, unless a push since did. */
    private fun flushChange() {
        synchronized(pushLock) {
            flushArmed = false
            if (seq <= pushedSeq) return
            sendChanged(pendingPkg, clock.uptimeMillis())
        }
    }

    /** Pushes the latest count. Called with [pushLock] held. */
    private fun sendChanged(pkg: String?, now: Long) {
        val s = seq
        lastPushedChangeAt = now
        pushedSeq = s
        pendingPkg = null
        push("{\"ev\":\"changed\",\"seq\":$s${if (pkg != null) ",\"pkg\":\"$pkg\"" else ""}}")
    }

    /**
     * A Toast is a window of its own that the windows list leaves out (it takes no touches);
     * its text comes in this event. [Toasts] keeps it on screen for a long Toast's time, and
     * its coming and going count as changes, so waits re-read the screen.
     */
    private fun onToast(e: UiEvent) {
        val texts = e.texts.filter { it.isNotBlank() }
        if (texts.isEmpty()) return
        val until = clock.uptimeMillis() + Toasts.SHOWN_MS
        Toasts.texts = texts
        Toasts.until = until
        changedNow(e.pkg)
        clock.postAt({ if (Toasts.until == until) changedNow(null) }, until + 1)
    }

    /** A solid change that no accessibility event of the app's carries (a Toast). */
    private fun changedNow(pkg: String?) {
        val now = clock.uptimeMillis()
        synchronized(lock) {
            seq++
            solid++
            lastEventAt = now
            lock.notifyAll()
        }
        pushChanged(pkg, now, force = true)
        clock.cancel(quietTimer)
        clock.postDelayed(quietTimer, quietMs.toLong())
    }

    private fun onQuiet() {
        val now = clock.uptimeMillis()
        val drewAt = lastFrameAt()
        // With animations off, the window drawing while something restless changes is that
        // thing drawing: holding for its frames would hold every action on a never-still screen
        // for FRAME_GRACE_MS. (With animations on, a sheet sliding in draws too: wait for it.)
        val restlessDrawing = animationsOff && now - lastRestlessAt < RESTLESS_DRAW_MS
        if (debug) Log.d(TAG, "quiet: drew ${now - drewAt} ms ago, last solid ${now - lastEventAt} ms ago, restless drawing $restlessDrawing")
        if (drewAt > 0 && now - drewAt < quietMs && now - lastEventAt < FRAME_GRACE_MS && !restlessDrawing) {
            clock.postDelayed(quietTimer, quietMs - (now - drewAt) + 1)
            return
        }
        // A change can arrive while this timer runs: the event thread re-arms the timer only
        // after its locked section, too late to stop this run. Settle only once a full quiet
        // window has passed since the latest solid change, read under the lock (issue #71).
        // The count is read and pushed under pushLock, so no "changed" with a higher count can
        // go out between them.
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
                    lock.notifyAll()
                }
                // else: already settled at this count. Two timers can be pending (this one
                // re-armed below as the change's own was posted): the second pushes nothing.
            }
            if (s >= 0) {
                if (s > pushedSeq) pushedSeq = s
                push("{\"ev\":\"settled\",\"seq\":$s}")
            }
        }
        // Re-armed here rather than left to the change's own re-post, so a settle never
        // depends on another thread's timing; the guard above drops the duplicate it can make.
        if (wait > 0) clock.postDelayed(quietTimer, wait)
    }

    private fun push(msg: String) {
        try { sink?.invoke(msg) } catch (_: Throwable) {}
    }

    /** Called before an action: frame stats then cover what the action makes the app draw. */
    fun beginAction() {
        try {
            val w = probe.activeWindowId()
            frameWindow = w
            if (w >= 0) probe.clearFrameStats(w)
        } catch (_: Throwable) {}
    }

    /** Uptime ms of the active window's latest frame since [beginAction], or 0. */
    private fun lastFrameAt(): Long {
        val w = frameWindow
        if (w < 0) return 0
        return try {
            val presented = probe.lastFramePresentedNanos(w)
            if (presented <= 0) return 0
            // Presented times are CLOCK_MONOTONIC, the same clock as uptime (in ms) plus deep sleep.
            val ageMs = (clock.nanoTime() - presented) / 1_000_000
            clock.uptimeMillis() - ageMs
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
        val start = clock.uptimeMillis()
        val deadline = start + timeoutMs
        synchronized(lock) {
            while (true) {
                val now = clock.uptimeMillis()
                if (solid == since) {
                    // The timeout can end the wait before noChangeMs does: lock.wait(0) never ends.
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

    /** Waits until no solid change is pending, up to timeoutMs. */
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

    /** Rechecks a condition only when the device sends an event, with no polling. */
    fun awaitCondition(timeoutMs: Int, condition: () -> Boolean): Boolean {
        val deadline = clock.uptimeMillis() + timeoutMs.coerceAtLeast(0)
        synchronized(lock) {
            while (!condition()) {
                val left = deadline - clock.uptimeMillis()
                if (left <= 0) return false
                clock.await(lock, left)
            }
        }
        return true
    }

    /** Waits until [pkg] owns the active window. Wakes on window events only. */
    fun awaitPackage(pkg: String, timeoutMs: Int): Boolean {
        val deadline = clock.uptimeMillis() + timeoutMs
        var checkedAt = -1L
        synchronized(lock) {
            while (true) {
                if (checkedAt != lastWindowChangeAt || checkedAt < 0) {
                    checkedAt = lastWindowChangeAt
                    val active = try { probe.activePackage() } catch (_: Throwable) { null }
                    if (active != null) foregroundPackage = active
                    if (active == pkg) return true
                }
                val left = deadline - clock.uptimeMillis()
                if (left <= 0) return false
                clock.await(lock, left)
            }
        }
        @Suppress("UNREACHABLE_CODE")
        return false
    }

    companion object {
        const val DEFAULT_QUIET_MS = 120
        /** At most one "changed" push per this many ms (the last of a burst at the window's end). */
        const val COALESCE_MS = 30L
        const val FRAME_GRACE_MS = 600
        const val RESTLESS_AFTER_MS = 500L
        const val RESTLESS_GAP_MS = 400L
        const val RESTLESS_MEMORY = 256
        const val RESTLESS_DRAW_MS = 250L
        /** `adb shell setprop log.tag.TestBuddyAgent DEBUG` logs every event and quiet check. */
        val debug: Boolean get() = Log.isLoggable(TAG, Log.DEBUG)
    }
}

/** The Toast showing, if any: its texts, and the uptime ms until which it counts as shown. */
object Toasts {
    /** Toast.LENGTH_LONG: a short Toast (2 s) counts as shown a little longer than it is. */
    const val SHOWN_MS = 3_500L
    @Volatile var texts: List<String> = emptyList()
    @Volatile var until = 0L

    fun showing(now: Long = SystemClock.uptimeMillis()): List<String> = if (now < until) texts else emptyList()
}

/** A window an app opened: its id, and when (uptime ms) its window-state event came. */
class OpenedWindow(val id: Int, val at: Long)
