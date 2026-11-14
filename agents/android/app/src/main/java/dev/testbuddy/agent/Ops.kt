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
        if (op == "tap" || op == "doubleTap" || op == "press") {
            val waited = awaitInputWindow(fx(req, "x"), fx(req, "y"), req.optBoolean("ci"))
            if (waited > 0) r.put("windowWaitMs", waited)
        }
        val since = events.seq
        val clicksBefore = events.clicks
        events.beginAction()
        val t0 = clock.uptimeMillis()
        if (op == "tap" || op == "doubleTap" || op == "press") lastTapAt = t0
        when (op) {
            "tap" -> input.tap(fx(req, "x"), fx(req, "y"))?.let { r.put("notInjected", it) }
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
                val typed = if (req.optBoolean("verifyFirst")) input.typeChecked(text, events, waitForFocus = req.optBoolean("ci")) else input.type(text)
                if (typed == null) {
                    r.put("verifyUnavailable", true).put("seq", events.seq)
                    return // nothing injected: the runner can safely use its original guard
                }
                if (!typed) throw IllegalStateException("no focused text field to type into")
            }
            "erase" -> input.erase(req.optInt("n", 50))
            "clear" -> r.put("cleared", input.clearFocused())
        }
        val t1 = clock.uptimeMillis()
        lastActionAt = t1
        r.put("injectMs", t1 - t0)
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

    private fun fx(req: JSONObject, k: String): Float = req.getDouble(k).toFloat()

    /**
     * Holds a touch at ([x], [y]) until the window it goes to is [inputWindowAgeMs] old (or
     * [CI_INPUT_WINDOW_AGE_MS] with [ci]), and says how long it waited (issue #204). Android
     * drops a touch sent into a window just added whose input channel is not up yet ("Not
     * sending touch gesture to ... NO_INPUT_CHANNEL"), and the app sees nothing of it. Only the
     * target window's own age counts: other windows coming or going (the keyboard, the status
     * bar, a dialog closing elsewhere) never delay a tap. With no window that young on screen
     * there is nothing to look up: no read, no wait.
     */
    private fun awaitInputWindow(x: Float, y: Float, ci: Boolean): Long {
        val need = if (ci) CI_INPUT_WINDOW_AGE_MS else inputWindowAgeMs
        val start = clock.uptimeMillis()
        if (start - events.windows.newest >= need) return 0
        val id = tree.windowAt(x, y)
        if (id < 0) return 0
        val seen = events.windows.firstSeen(id)
        if (seen <= 0) return 0
        val wait = seen + need - start
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

    /** How old a window must be before a touch goes into it (issue #204; see [awaitInputWindow]). */
    var inputWindowAgeMs = INPUT_WINDOW_AGE_MS

    companion object {
        const val RECENT_REQUESTS = 64
        /**
         * A new window's input channel comes up with the transaction that shows it: within a
         * frame or two of the window being listed on a device that keeps up. 100 ms covers that
         * several times over; it costs at most that, once, on the first tap into a new window.
         */
        const val INPUT_WINDOW_AGE_MS = 100L
        /**
         * A CI allowance: on a slow emulator system_server runs several times slower and a
         * window was listed before it could take input (issue #204, a tap right after launch).
         */
        const val CI_INPUT_WINDOW_AGE_MS = 400L
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
