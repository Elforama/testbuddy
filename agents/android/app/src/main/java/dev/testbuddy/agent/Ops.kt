package dev.testbuddy.agent

import android.util.Log
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * The protocol's ops (see the host's internal/wire package): one request in, one reply out.
 * The device is behind [Device], [Screen], [Input] and [Events], so this runs in unit tests
 * against fakes; [Server] carries requests and replies over the socket.
 */
class Ops(
    private val device: Device,
    private val tree: Tree,
    private val input: Input,
    private val events: Events,
    private val clock: Clock,
    /** Runs work off the request path (the location's repeats); inline in tests. */
    background: (Runnable) -> Unit = { it.run() },
) {
    private val location = Location(device, clock, background)

    /** A reply line, and the binary data that follows it (a screenshot), if any. */
    class Reply(val text: String, val bin: ByteArray? = null)

    /** How long the agent stays with no client: short unless a run asked to keep it ("keep"). */
    @Volatile var idleLimitMs = Server.ORPHAN_EXIT_MS
        private set

    /** Uptime ms when the last input action ended (its injection, then its settle wait). */
    @Volatile private var lastActionAt = 0L
    /** Uptime ms when the last tap, double tap or long press was sent, or -1. */
    @Volatile private var lastTapAt = -1L

    /** How events go in ("input-manager" or "ui-automation"). */
    val injectPath: String get() = input.path

    /** The first line a client gets. */
    fun hello(): String {
        val size = device.displaySize()
        return Json().obj().put("ev", "hello").put("platform", "android").put("version", AGENT_VERSION)
            .put("sdk", device.sdk).put("w", size[0]).put("h", size[1])
            .put("density", device.density)
            .put("inject", input.path).put("seq", events.seq)
            // A mock location on: the host turns it off if it did not set it (issue #72).
            .put("location", location.isOn).endObj().toString()
    }

    /**
     * Answers one request line; null for a line that is no request (blank or not JSON).
     * [client] is the connection it came on (see [clientGone]).
     */
    fun handle(line: String, client: Any? = null): Reply? {
        if (line.isBlank()) return null
        val req = try { JSONObject(line) } catch (_: Throwable) { return null }
        return handle(req, client)
    }

    /** The connection [client] closed: what it alone kept going ends, after a grace (Location.released). */
    fun clientGone(client: Any) {
        try { location.released(client) } catch (t: Throwable) { Log.w(TAG, "location release", t) }
    }

    /** The agent is about to exit: nothing it set on the device stays behind (the mock location). */
    fun shutdown() {
        try { location.off() } catch (t: Throwable) { Log.w(TAG, "location off", t) }
    }

    fun handle(req: JSONObject, client: Any? = null): Reply {
        val id = req.optLong("id")
        // An action the host sends again after its connection dropped carries the same
        // request token: answer it from the first run (waiting for it if it is still
        // going) instead of tapping or typing twice.
        val rid = req.optString("rid", "")
        var done: Done? = null
        if (rid.isNotEmpty()) {
            val earlier: Done?
            synchronized(recent) {
                earlier = recent[rid]
                if (earlier == null) { done = Done(); recent[rid] = done!! }
            }
            if (earlier != null) {
                Log.i(TAG, "request $rid sent again: answering from its first run")
                if (!earlier.latch.await(120, TimeUnit.SECONDS)) {
                    return Reply(Json().obj().put("id", id).put("ok", false).put("error", "the first run of this request did not finish").endObj().toString())
                }
                return Reply(withId(earlier.reply!!, id), earlier.bin)
            }
        }
        val reply = Json(1024)
        var bin: ByteArray? = null
        try {
            reply.obj().put("id", id).put("ok", true)
            bin = dispatch(req, reply, client)
            if (bin != null) reply.put("bin", bin.size)
            reply.endObj()
        } catch (t: Throwable) {
            val c = cause(t)
            Log.w(TAG, "op ${req.optString("op")} failed", c)
            reply.reset().obj().put("id", id).put("ok", false).put("error", c.message ?: c.toString()).endObj()
            bin = null
        }
        val text = reply.toString()
        done?.let { it.reply = text; it.bin = bin; it.latch.countDown() }
        return Reply(text, bin)
    }

    /** A request's reply, kept for a re-send of the same request token. */
    private class Done {
        val latch = CountDownLatch(1)
        @Volatile var reply: String? = null
        @Volatile var bin: ByteArray? = null
    }

    /** The replies to the last [RECENT_REQUESTS] tokened requests, across connections. */
    private val recent = object : LinkedHashMap<String, Done>(64, 0.75f, false) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Done>) = size > RECENT_REQUESTS
    }

    /** Runs one request, writing its result fields into [r]. Returns binary data, if any. */
    private fun dispatch(req: JSONObject, r: Json, client: Any?): ByteArray? {
        when (val op = req.optString("op")) {
            "ping" -> r.put("seq", events.seq)
            "tree" -> {
                if (req.optBoolean("waitQuiet", false)) events.awaitQuiet(req.optInt("timeout", 3000))
                r.put("seq", events.seq)
                val profile = if (req.optBoolean("profile")) ReadProfile { clock.nanoTime() } else null
                val keyboard = tree.write(r, req.optBoolean("all", false), req.optBoolean("fresh", false),
                    profile, req.optBoolean("prefetchChildren", true), req.optBoolean("includeHidden"))
                r.put("keyboard", keyboard)
                profile?.write(r)
            }
            "tap", "doubleTap", "press", "swipe", "key", "type", "erase", "clear" -> action(op, req, r)
            "settle" -> {
                val res = events.awaitSettled(req.optLong("since", events.seq), req.optInt("noChange", 300), req.optInt("timeout", 5000), req.optBoolean("react"))
                r.put("result", res.name.lowercase()).put("seq", events.seq)
            }
            "quiet" -> r.put("quiet", events.awaitQuiet(req.optInt("timeout", 5000))).put("seq", events.seq)
            "awaitPackage" -> r.put("found", events.awaitPackage(req.getString("pkg"), req.optInt("timeout", 10000)))
            "foreground" -> r.put("pkg", try { device.foregroundPackage() } catch (_: Throwable) { null })
            "editorTarget" -> {
                // The change count before the editor's Backspace, which the host settles on
                // ("settle"'s since). A seq, as everywhere else (issue #74: it was the solid count;
                // an agent before 0.1.48 still sends that, and its "settle" compares with it).
                val seq = events.seq
                val target = tree.focusedInput()?.editorTarget()
                r.put("seq", seq)
                if (target != null) {
                    events.beginAction()
                    r.key("target").obj().put("pkg", target.pkg).put("window", target.window)
                        .put("node", target.node).put("id", target.id).put("cls", target.cls).endObj()
                }
            }
            "screenshot" -> return screenshot(req, r)
            "sh" -> {
                val res = device.shell(req.getString("cmd"), req.optInt("timeout", 30000))
                r.put("code", res.code).put("out", res.out)
                if (res.timedOut) r.put("timedOut", true)
            }
            "info" -> {
                val size = device.displaySize()
                r.put("w", size[0]).put("h", size[1]).put("sdk", device.sdk)
                    .put("rotation", device.rotation()).put("keyboard", tree.keyboardShown())
            }
            "hideKeyboard" -> {
                // Check on the device immediately before Back: a hidden IME must
                // never turn this command into navigation in the application.
                val timeout = req.optInt("timeout", 3000)
                val sent = tree.keyboardShown()
                if (sent) input.key(android.view.KeyEvent.KEYCODE_BACK)
                var hidden = events.awaitCondition(timeout) { !tree.keyboardShown() }
                // Issue #175: a field focused by the last tap, or by the screen it opened, can
                // still be bringing its keyboard up (a slow device shows it a second or more
                // after the tap), and it then stays up after this returned. Only when no read
                // has found the keyboard up since the last tap (once it has shown, it is not
                // still on its way: Enter, a second hideKeyboard, a custom keypad pay nothing)
                // does it wait for one: until pendingMs after the last action, or after the
                // screen's last change while it keeps changing (a screen still opening reports
                // no focused field yet), up to twice pendingMs. Once it shows and stays for
                // KEYBOARD_STEADY_MS, it is closed as above.
                // Known gap: a tap that opens a screen whose field takes the focus, read while
                // the old screen's keyboard is still animating away, counts as "seen since the
                // tap", and then hideKeyboard does not wait for the new screen's keyboard.
                val pending = req.optInt("pendingMs", 0)
                val start = clock.uptimeMillis()
                var late = false
                if (hidden && !sent && lastTapAt >= 0 && tree.keyboardSeenAt < lastTapAt &&
                    start - lastTapAt < TAP_RECENT_MS && lastActionAt + pending > start) {
                    while (!late) {
                        val until = minOf(start + 2L * pending, maxOf(lastActionAt, events.lastEventAt) + pending)
                        val left = until - clock.uptimeMillis()
                        if (left <= 0) break
                        if (!events.awaitCondition(left.toInt()) { tree.keyboardShown() }) continue
                        // A keyboard that flickers up and away must not turn Back into navigation.
                        late = !events.awaitCondition(KEYBOARD_STEADY_MS) { !tree.keyboardShown() } && tree.keyboardShown()
                    }
                }
                if (late) {
                    input.key(android.view.KeyEvent.KEYCODE_BACK)
                    lastActionAt = clock.uptimeMillis()
                    hidden = events.awaitCondition(timeout) { !tree.keyboardShown() }
                    r.put("late", true)
                }
                r.put("hidden", hidden).put("seq", events.seq)
            }
            "clicks" -> r.put("clicks", events.clicks)
            "awaitCoverGone" -> {
                val window = req.getInt("window")
                val pkg = req.getString("pkg")
                val nodeId = req.getString("nodeId")
                val cls = req.getString("class")
                val x = req.getInt("x"); val y = req.getInt("y")
                fun present() = tree.coverPresent(window, pkg, nodeId, cls, x, y)
                val observed = present()
                var initial = true
                val gone = !observed || events.awaitCondition(req.optInt("timeout", 3000)) {
                    if (initial) { initial = false; false } else !present()
                }
                r.put("observed", observed).put("gone", gone).put("seq", events.seq)
            }
            "setQuiet" -> { events.quietMs = req.getInt("ms").coerceIn(50, 5000); r.put("quiet", events.quietMs) }
            "animations" -> { events.animationsOff = !req.getBoolean("on"); r.put("on", !events.animationsOff) }
            "location" -> if (req.optBoolean("off")) {
                location.off()
                r.put("off", true)
            } else {
                val on = location.set(req.getDouble("lat"), req.getDouble("lon"), req.optLong("everyMs", Location.DEFAULT_EVERY_MS), client)
                r.key("providers").arr()
                on.forEach { r.str(it) }
                r.endArr()
            }
            "rotate" -> device.rotate(Rotation.of(req.optString("to")))
            "exit" -> { shutdown(); r.put("bye", true); device.exitSoon() }
            "keep" -> { idleLimitMs = Server.IDLE_EXIT_MS; r.put("idleMs", idleLimitMs) }
            else -> throw IllegalArgumentException("unknown op \"$op\"")
        }
        return null
    }

    /**
     * Runs an input action, then (unless settle=false) waits for the app to react: the UI
     * changed and went quiet, or nothing changed within noChange ms. Waiting is event-driven.
     */
    private fun action(op: String, req: JSONObject, r: Json) {
        val touch = op == "tap" || op == "doubleTap" || op == "press"
        // The window the input goes to, when one on screen is young (issue #204, #280): none
        // when no window is, and then nothing is read. A window older than that never counts
        // as coming in (its age runs from when it first came, WindowAges), so its input need
        // not be noted.
        val newest = events.windows.newest
        val young = newest > 0 && clock.uptimeMillis() - newest < FIRST_INPUT_HORIZON_MS
        val listed = if (young && (touch || op == "swipe")) tree.windowList() else emptyList()
        val target: UiWindow? = when {
            listed.isEmpty() -> null
            touch -> tree.windowAt(listed, fx(req, "x"), fx(req, "y"))
            else -> tree.windowAt(listed, fx(req, "x1"), fx(req, "y1"))
        }
        val window = when {
            target != null -> target.id
            young && (op == "key" || op == "type" || op == "erase" || op == "clear") -> events.activeWindowId()
            else -> -1
        }
        if (touch) {
            val waited = awaitInputWindow(window, req.optBoolean("ci"))
            if (waited > 0) r.put("windowWaitMs", waited)
            // Issue #321: where the node the host aims at (by its read, window and place) is
            // right before the DOWN, asked of its app (refreshed: not the node cache, not events:
            // a translation sends none, and a content event can come ~100 ms late). Under the
            // point, the touch landed on it, and the host does not take a later move of it for a
            // layout shift that made the tap miss.
            req.optJSONObject("aim")?.let { aim ->
                val a0 = clock.uptimeMillis()
                tree.aimAt(aim.optLong("read"), aim.optInt("window", -1), aim.optInt("ref"))?.let { b ->
                    r.key("aimAt").arr().num(b.left).num(b.top).num(b.right).num(b.bottom).endArr()
                }
                r.put("aimMs", clock.uptimeMillis() - a0)
            }
        }
        val since = events.seq
        val clicksBefore = events.clicks
        var notInjected: String? = null
        events.beginAction()
        val t0 = clock.uptimeMillis()
        if (op == "tap" || op == "doubleTap" || op == "press") lastTapAt = t0
        when (op) {
            "tap" -> input.tap(fx(req, "x"), fx(req, "y"))?.let { notInjected = it; r.put("notInjected", it) }
            "doubleTap" -> input.doubleTap(fx(req, "x"), fx(req, "y"))
            "press" -> input.press(fx(req, "x"), fx(req, "y"), req.optLong("ms", 3000))
            "swipe" -> if (req.optBoolean("drag")) {
                input.drag(fx(req, "x1"), fx(req, "y1"), fx(req, "x2"), fx(req, "y2"))
            } else {
                input.swipe(fx(req, "x1"), fx(req, "y1"), fx(req, "x2"), fx(req, "y2"), req.optLong("ms", 400))
            }
            "key" -> {
                if (req.has("expectedEditor")) {
                    val expected = EditorTarget.fromJson(req.getJSONObject("expectedEditor"))
                    check(tree.focusedInput()?.editorTarget() == expected) { "editor focus changed before hardware fallback" }
                }
                r.put("keyPath", input.key(req.getInt("code"), req.optBoolean("long", false)))
            }
            "type" -> {
                val text = req.getString("text")
                val checked = req.optBoolean("verifyFirst")
                // CI mode: the wait for focus (issue #97) and the longer wait per key (issue #279).
                val ci = req.optBoolean("ci")
                val typed = if (checked) input.typeChecked(text, events, waitForFocus = ci, ci = ci) else input.type(text)
                if (typed == null) {
                    r.put("verifyUnavailable", true).put("seq", events.seq)
                    return // nothing injected: the runner can safely use its original guard
                }
                if (!typed) throw IllegalStateException("no focused text field to type into")
                // Issue #279: how the keys went in, when not in chunks ("acknowledged", "repaired").
                if (checked) input.lastTyping?.let { r.put("typing", it) }
            }
            "erase" -> {
                // Issue #289: the caret goes to the end, and the field is read back after; CI
                // mode waits longer for it, as for typing.
                input.erase(req.optInt("n", 50), events, ci = req.optBoolean("ci"))
                input.lastErase?.let { r.put("erase", it) }
            }
            "clear" -> r.put("cleared", input.clearFocused())
        }
        val t1 = clock.uptimeMillis()
        lastActionAt = t1
        r.put("injectMs", t1 - t0)
        // Issue #280: whether this was the first input the window ever got, its age then, and
        // whether it came over the app (a touch's only: the host may send a tap that provably
        // did nothing again). A DOWN the platform refused reached no window.
        if (window >= 0 && notInjected != "down") {
            val seen = events.windows.firstAppeared(window)
            val first = events.windows.tookInput(window)
            if (touch && seen > 0 && target != null) {
                r.put("firstInWindow", first).put("windowAgeMs", t0 - seen).put("windowOver", comesOver(target))
            }
        }
        lastInputPackage = target?.let { packageOf(it) } ?: events.foregroundPackage ?: lastInputPackage
        if (req.optBoolean("settle", true)) {
            val res = events.awaitSettled(since, req.optInt("noChange", 250), req.optInt("timeout", 5000), req.optBoolean("react"))
            r.put("result", res.name.lowercase()).put("settleMs", clock.uptimeMillis() - t1)
            lastActionAt = clock.uptimeMillis()
        }
        // Whether a view reported a click by now: a tap that reached nothing reports none. The
        // event can come after this reply (once the view's handler ran), so the count before
        // the action goes back too, for the host to compare later ("clicks").
        r.put("clicked", events.clicks > clicksBefore).put("clicksBefore", clicksBefore)
        r.put("seq", events.seq)
    }

    /** The package the last input went to (issue #280), null before any. */
    @Volatile private var lastInputPackage: String? = null

    private fun packageOf(w: UiWindow): String? = try { w.root()?.packageName?.toString() } catch (_: Throwable) { null }

    /**
     * Issue #280: whether [w] came in over the app rather than being its screen. Neither its
     * size (split screen, freeform windows and landscape with 3-button navigation make an
     * Activity's window smaller than the display, and a bottom sheet's covers it) nor the
     * windows listed under it (a modal dialog takes its Activity's window off the list while it
     * is up) can tell. What opened it does: the class of its window-state event, an Activity of
     * its package (declared in its manifest), or else a Dialog, a sheet, a popup. Such a window
     * is over the app when it came while a window of its own package, or of the package the
     * last input went to, was listed (a dialog over its Activity). An app's own Activity never
     * is (a new screen, whatever its style: a press there that acts late and draws nothing must
     * not be sent twice), nor another app launched; nor is a window whose opening was not seen.
     *
     * Issue #317: an Activity of another package is over the app when it floats on it
     * ([floatsOnTheApp]): Android's runtime permission dialog, the permission controller's
     * GrantPermissionsActivity, which takes no touch while it comes in (tapjacking protection).
     *
     * Only a window over the app may take no touch while it comes in (a guard against taps meant
     * for the screen under it); an Activity's known way of losing a touch (NO_INPUT_CHANNEL) is
     * reported as notInjected (issue #204).
     */
    private fun comesOver(w: UiWindow): Boolean {
        val seen = events.windows.openedBy(w.id)
        val first = seen.firstOrNull() ?: return false
        val pkg = packageOf(w) ?: first.pkg ?: return false
        // Fails closed (issue #280 re-review): an event whose class cannot be told makes it a
        // screen. Only the event of the window's creation names what opened it: the first one,
        // within OPENED_EVENT_MS of the window coming, and not a view's class (a drawer's or
        // pane's event on an Activity whose own was missed).
        val kinds = seen.map { e -> e.cls?.let { device.isActivity(pkg, it) } }
        if (kinds.any { it == null }) return false
        val came = events.windows.firstAppeared(w.id)
        if (came <= 0 || kotlin.math.abs(first.at - came) > OPENED_EVENT_MS) return false
        // Any event naming an Activity of the package makes it a screen, unless that Activity,
        // opening the window, floats on the app (issue #317).
        if (kinds.any { it == true }) return kinds[0] == true && floatsOnTheApp(w, pkg)
        if (viewClass(first.cls!!)) return false
        val under = events.windows.listedBefore(w.id).mapNotNull { events.windows.openedBy(it).firstOrNull()?.pkg }
        return pkg in under || (lastInputPackage != null && lastInputPackage in under)
    }

    /**
     * Issue #317: whether [w], an Activity's window of [pkg], floats on the app: [pkg] is not
     * the package the last input went to, and [w] came while a window of that package was
     * listed, inset on every side within that window's bounds. A dialog-styled Activity of
     * another app opened over it (the permission controller's dialog; seen on an emulator, the
     * app's window then drops off the list). Not another app launched (its window covers the
     * app's), nor one beside it (split screen), nor one that came over no window of the app; nor
     * the app's own Activity, whatever its style (a new screen of the app, issue #280).
     */
    private fun floatsOnTheApp(w: UiWindow, pkg: String): Boolean {
        val app = lastInputPackage ?: return false
        if (pkg == app) return false
        val box = w.bounds
        return events.windows.listedBefore(w.id).any { id ->
            val under = events.windows.boundsOf(id)
            events.windows.openedBy(id).firstOrNull()?.pkg == app && under != null &&
                box.left > under.left && box.top > under.top && box.right < under.right && box.bottom < under.bottom
        }
    }

    /** A view's class, not a window's: the framework's and libraries' views and layouts. */
    private fun viewClass(cls: String): Boolean =
        cls.startsWith("android.view.") || cls.startsWith("android.widget.") ||
            cls.endsWith("Layout") || cls.endsWith("View") || cls.endsWith("Pane")

    private fun fx(req: JSONObject, k: String): Float = req.getDouble(k).toFloat()

    /**
     * With [ci] (the host's CI mode), holds a touch into [window] (-1: none young under it) until
     * it is [inputWindowAgeMs] old, and says how long it waited (issue #204). A CI allowance: #204
     * was seen on a CI emulator only, so without [ci] the touch goes at once, as before. Android
     * drops a touch sent into a window just added whose input channel is not up yet ("Not
     * sending touch gesture to ... NO_INPUT_CHANNEL"), and the app sees nothing of it. Only the
     * target window's own age counts: other windows coming or going (the keyboard, the status
     * bar, a dialog closing elsewhere) never delay a tap.
     */
    private fun awaitInputWindow(window: Int, ci: Boolean): Long {
        if (!ci || window < 0) return 0
        val start = clock.uptimeMillis()
        val seen = events.windows.firstSeen(window)
        if (seen <= 0) return 0
        val wait = seen + inputWindowAgeMs - start
        if (wait <= 0) return 0
        clock.sleep(wait)
        return clock.uptimeMillis() - start
    }

    private fun screenshot(req: JSONObject, r: Json): ByteArray {
        val fmt = req.optString("format", "png")
        val shot = device.screenshot(req.optDouble("scale", 1.0), if (fmt == "jpg") "jpeg" else fmt, req.optInt("quality", 80))
        r.put("w", shot.width).put("h", shot.height).put("format", if (fmt == "jpg") "jpeg" else fmt)
        return shot.bytes
    }

    /** How old a window must be before a touch goes into it in CI mode (issue #204; see [awaitInputWindow]). */
    var inputWindowAgeMs = CI_INPUT_WINDOW_AGE_MS

    companion object {
        const val RECENT_REQUESTS = 64
        /**
         * A CI allowance: on a slow emulator system_server runs several times slower and a
         * window was listed before it could take input (issue #204, a tap right after launch).
         * On a device that keeps up the input channel comes up within a frame or two of the
         * window being listed, and by the time tb has found its target the window is older.
         */
        const val CI_INPUT_WINDOW_AGE_MS = 400L
        /**
         * Issue #280: input into a window younger than this says whether it was the window's
         * first (firstInWindow), its age, and whether it came over the app. Older windows are
         * not ones coming in (a dialog's guard lasts about a second): nothing is read for them,
         * nor said.
         */
        const val FIRST_INPUT_HORIZON_MS = 1_500L
        /** How close to a window's coming its window-state event must be to name what opened it. */
        const val OPENED_EVENT_MS = 1_000L
        /** How long a keyboard that came late must stay up before hideKeyboard closes it. */
        const val KEYBOARD_STEADY_MS = 100
        /** A tap older than this brings no keyboard up any more (hideKeyboard's wait). */
        const val TAP_RECENT_MS = 10_000L

        /** A reply ({"id":N,...}) re-addressed to request id [id]. */
        fun withId(reply: String, id: Long): String {
            val comma = reply.indexOf(',')
            return if (comma < 0) reply else "{\"id\":$id" + reply.substring(comma)
        }
    }
}
