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
) {
    /** A reply line, and the binary data that follows it (a screenshot), if any. */
    class Reply(val text: String, val bin: ByteArray? = null)

    /** How long the agent stays with no client: short unless a run asked to keep it ("keep"). */
    @Volatile var idleLimitMs = Server.ORPHAN_EXIT_MS
        private set

    /** How events go in ("input-manager" or "ui-automation"). */
    val injectPath: String get() = input.path

    /** The first line a client gets. */
    fun hello(): String {
        val size = device.displaySize()
        return Json().obj().put("ev", "hello").put("platform", "android").put("version", AGENT_VERSION)
            .put("sdk", device.sdk).put("w", size[0]).put("h", size[1])
            .put("density", device.density)
            .put("inject", input.path).put("seq", events.seq).endObj().toString()
    }

    /** Answers one request line; null for a line that is no request (blank or not JSON). */
    fun handle(line: String): Reply? {
        if (line.isBlank()) return null
        val req = try { JSONObject(line) } catch (_: Throwable) { return null }
        return handle(req)
    }

    fun handle(req: JSONObject): Reply {
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
            bin = dispatch(req, reply)
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
    private fun dispatch(req: JSONObject, r: Json): ByteArray? {
        when (val op = req.optString("op")) {
            "ping" -> r.put("seq", events.seq)
            "tree" -> {
                if (req.optBoolean("waitQuiet", false)) events.awaitQuiet(req.optInt("timeout", 3000))
                r.put("seq", events.seq)
                tree.write(r, req.optBoolean("all", false), req.optBoolean("fresh", false))
                r.put("keyboard", tree.keyboardShown())
            }
            "tap", "doubleTap", "press", "swipe", "key", "type", "erase", "clear" -> action(op, req, r)
            "settle" -> {
                val res = events.awaitSettled(req.optLong("since", events.solid), req.optInt("noChange", 300), req.optInt("timeout", 5000))
                r.put("result", res.name.lowercase()).put("seq", events.seq)
            }
            "quiet" -> r.put("quiet", events.awaitQuiet(req.optInt("timeout", 5000))).put("seq", events.seq)
            "awaitPackage" -> r.put("found", events.awaitPackage(req.getString("pkg"), req.optInt("timeout", 10000)))
            "foreground" -> r.put("pkg", try { device.foregroundPackage() } catch (_: Throwable) { null })
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
            "clicks" -> r.put("clicks", events.clicks)
            "setQuiet" -> { events.quietMs = req.getInt("ms").coerceIn(50, 5000); r.put("quiet", events.quietMs) }
            "animations" -> { events.animationsOff = !req.getBoolean("on"); r.put("on", !events.animationsOff) }
            "rotate" -> device.rotate(Rotation.of(req.optString("to")))
            "exit" -> { r.put("bye", true); device.exitSoon() }
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
        val since = events.solid
        val clicksBefore = events.clicks
        events.beginAction()
        val t0 = clock.uptimeMillis()
        when (op) {
            "tap" -> input.tap(fx(req, "x"), fx(req, "y"))
            "doubleTap" -> input.doubleTap(fx(req, "x"), fx(req, "y"))
            "press" -> input.press(fx(req, "x"), fx(req, "y"), req.optLong("ms", 3000))
            "swipe" -> if (req.optBoolean("drag")) {
                input.drag(fx(req, "x1"), fx(req, "y1"), fx(req, "x2"), fx(req, "y2"))
            } else {
                input.swipe(fx(req, "x1"), fx(req, "y1"), fx(req, "x2"), fx(req, "y2"), req.optLong("ms", 400))
            }
            "key" -> input.key(req.getInt("code"), req.optBoolean("long", false))
            "type" -> if (!input.type(req.getString("text"))) throw IllegalStateException("no focused text field to type into")
            "erase" -> input.erase(req.optInt("n", 50))
            "clear" -> r.put("cleared", input.clearFocused())
        }
        val t1 = clock.uptimeMillis()
        r.put("injectMs", t1 - t0)
        if (req.optBoolean("settle", true)) {
            val res = events.awaitSettled(since, req.optInt("noChange", 250), req.optInt("timeout", 5000), req.optBoolean("react"))
            r.put("result", res.name.lowercase()).put("settleMs", clock.uptimeMillis() - t1)
        }
        // Whether a view reported a click by now: a tap that reached nothing reports none. The
        // event can come after this reply (once the view's handler ran), so the count before
        // the action goes back too, for the host to compare later ("clicks").
        r.put("clicked", events.clicks > clicksBefore).put("clicksBefore", clicksBefore)
        r.put("seq", events.seq)
    }

    private fun fx(req: JSONObject, k: String): Float = req.getDouble(k).toFloat()

    private fun screenshot(req: JSONObject, r: Json): ByteArray {
        val fmt = req.optString("format", "png")
        val shot = device.screenshot(req.optDouble("scale", 1.0), if (fmt == "jpg") "jpeg" else fmt, req.optInt("quality", 80))
        r.put("w", shot.width).put("h", shot.height).put("format", if (fmt == "jpg") "jpeg" else fmt)
        return shot.bytes
    }

    companion object {
        const val RECENT_REQUESTS = 64

        /** A reply ({"id":N,...}) re-addressed to request id [id]. */
        fun withId(reply: String, id: Long): String {
            val comma = reply.indexOf(',')
            return if (comma < 0) reply else "{\"id\":$id" + reply.substring(comma)
        }
    }
}
