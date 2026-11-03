package dev.testbuddy.agent

import android.app.UiAutomation
import android.graphics.Bitmap
import android.graphics.Point
import android.net.LocalServerSocket
import android.net.LocalSocket
import android.os.Build
import android.os.SystemClock
import android.util.Log
import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.ByteArrayOutputStream
import java.io.OutputStream
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.system.exitProcess

/**
 * Serves one host at a time on the abstract socket [SOCKET_NAME]. A new connection replaces
 * the old one. Only the shell (adbd) and root may connect: the socket checks the peer's uid,
 * so other apps on the device cannot drive it.
 *
 * Protocol: one JSON object per line each way (see the host's internal/wire package).
 */
class Server(private val ua: UiAutomation, private val events: Events) {
    private val tree = Tree(ua)
    private val input = Input(ua, tree)
    private val writeLock = Object()
    @Volatile private var out: OutputStream? = null
    @Volatile private var lastClientAt = SystemClock.uptimeMillis()
    @Volatile private var clients = 0

    fun serve() {
        val server = LocalServerSocket(SOCKET_NAME)
        Log.i(TAG, "listening on @$SOCKET_NAME, injection: ${input.path}")
        println("ready")
        startIdleWatch()
        var current: LocalSocket? = null
        while (true) {
            val sock = server.accept()
            val uid = try { sock.peerCredentials.uid } catch (_: Throwable) { -1 }
            if (uid != 0 && uid != 2000) {
                Log.w(TAG, "refused connection from uid $uid")
                sock.close()
                continue
            }
            try { current?.close() } catch (_: Throwable) {}
            current = sock
            Thread({ handle(sock) }, "tb-client").start()
        }
    }

    /** Exits after IDLE_EXIT_MS with no client, so a forgotten agent does not linger. */
    private fun startIdleWatch() {
        Thread({
            while (true) {
                Thread.sleep(60_000) // housekeeping only; never on a test's path
                if (clients == 0 && SystemClock.uptimeMillis() - lastClientAt > IDLE_EXIT_MS) {
                    Log.i(TAG, "idle, exiting")
                    disableNoopService()
                    exitProcess(0)
                }
            }
        }, "tb-idle").apply { isDaemon = true }.start()
    }

    private fun handle(sock: LocalSocket) {
        clients++
        val os = BufferedOutputStream(sock.outputStream, 64 * 1024)
        synchronized(writeLock) { out = os }
        events.sink = { msg -> try { send(os, msg) } catch (_: Throwable) {} }
        try {
            val size = displaySize()
            send(os, Json().obj().put("ev", "hello").put("platform", "android").put("version", AGENT_VERSION)
                .put("sdk", Build.VERSION.SDK_INT).put("w", size.x).put("h", size.y)
                .put("density", android.content.res.Resources.getSystem().displayMetrics.density.toDouble())
                .put("inject", input.path).put("seq", events.seq).endObj().toString())
            val reader = BufferedInputStream(sock.inputStream).bufferedReader()
            while (true) {
                val line = reader.readLine() ?: break
                if (line.isBlank()) continue
                val req = try { JSONObject(line) } catch (t: Throwable) { continue }
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
                            send(os, Json().obj().put("id", id).put("ok", false).put("error", "the first run of this request did not finish").endObj().toString())
                        } else {
                            send(os, withId(earlier.reply!!, id), earlier.bin)
                        }
                        continue
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
                send(os, text, bin)
            }
        } catch (_: Throwable) {
        } finally {
            synchronized(writeLock) { if (out === os) { out = null; events.sink = null } }
            try { sock.close() } catch (_: Throwable) {}
            clients--
            lastClientAt = SystemClock.uptimeMillis()
        }
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

    /** A reply ({"id":N,...}) re-addressed to request id [id]. */
    private fun withId(reply: String, id: Long): String {
        val comma = reply.indexOf(',')
        return if (comma < 0) reply else "{\"id\":$id" + reply.substring(comma)
    }

    private fun send(os: OutputStream, line: String, bin: ByteArray? = null) {
        synchronized(writeLock) {
            os.write(line.toByteArray(Charsets.UTF_8))
            os.write('\n'.code)
            if (bin != null) os.write(bin)
            os.flush()
        }
    }

    /** Runs one request, writing its result fields into [r]. Returns binary data, if any. */
    private fun dispatch(req: JSONObject, r: Json): ByteArray? {
        when (val op = req.optString("op")) {
            "ping" -> r.put("seq", events.seq)
            "tree" -> {
                if (req.optBoolean("waitQuiet", false)) events.awaitQuiet(req.optInt("timeout", 3000))
                r.put("seq", events.seq)
                tree.write(r, req.optBoolean("all", false))
                r.put("keyboard", tree.keyboardShown())
            }
            "tap", "doubleTap", "press", "swipe", "key", "type", "erase", "clear" -> action(op, req, r)
            "settle" -> {
                val res = events.awaitSettled(req.optLong("since", events.solid), req.optInt("noChange", 300), req.optInt("timeout", 5000))
                r.put("result", res.name.lowercase()).put("seq", events.seq)
            }
            "quiet" -> r.put("quiet", events.awaitQuiet(req.optInt("timeout", 5000))).put("seq", events.seq)
            "awaitPackage" -> r.put("found", events.awaitPackage(req.getString("pkg"), req.optInt("timeout", 10000)))
            "foreground" -> r.put("pkg", try { ua.rootInActiveWindow?.packageName?.toString() } catch (_: Throwable) { null })
            "screenshot" -> return screenshot(req, r)
            "sh" -> shell(req.getString("cmd"), req.optInt("timeout", 30000), r)
            "info" -> {
                val size = displaySize()
                r.put("w", size.x).put("h", size.y).put("sdk", Build.VERSION.SDK_INT)
                    .put("rotation", ua.rotation()).put("keyboard", tree.keyboardShown())
            }
            "setQuiet" -> { events.quietMs = req.getInt("ms").coerceIn(50, 5000); r.put("quiet", events.quietMs) }
            "animations" -> { events.animationsOff = !req.getBoolean("on"); r.put("on", !events.animationsOff) }
            "rotate" -> {
                ua.setRotation(when (req.optString("to")) {
                    "landscape", "landscapeLeft" -> UiAutomation.ROTATION_FREEZE_90
                    "landscapeRight" -> UiAutomation.ROTATION_FREEZE_270
                    "upsideDown" -> UiAutomation.ROTATION_FREEZE_180
                    "portrait" -> UiAutomation.ROTATION_FREEZE_0
                    else -> UiAutomation.ROTATION_UNFREEZE
                })
            }
            "exit" -> { r.put("bye", true); Thread { disableNoopService(); exitProcess(0) }.start() }
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
        events.beginAction()
        val t0 = SystemClock.uptimeMillis()
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
        val t1 = SystemClock.uptimeMillis()
        r.put("injectMs", t1 - t0)
        if (req.optBoolean("settle", true)) {
            val res = events.awaitSettled(since, req.optInt("noChange", 250), req.optInt("timeout", 5000), req.optBoolean("react"))
            r.put("result", res.name.lowercase()).put("settleMs", SystemClock.uptimeMillis() - t1)
        }
        r.put("seq", events.seq)
    }

    private fun fx(req: JSONObject, k: String): Float = req.getDouble(k).toFloat()

    private fun screenshot(req: JSONObject, r: Json): ByteArray {
        val bmp = ua.takeScreenshot() ?: throw IllegalStateException("screenshot failed")
        val scale = req.optDouble("scale", 1.0)
        val img = if (scale in 0.05..0.99) {
            Bitmap.createScaledBitmap(bmp, (bmp.width * scale).toInt().coerceAtLeast(1), (bmp.height * scale).toInt().coerceAtLeast(1), true)
        } else bmp
        val fmt = req.optString("format", "png")
        val bos = ByteArrayOutputStream(256 * 1024)
        if (fmt == "jpeg" || fmt == "jpg") img.compress(Bitmap.CompressFormat.JPEG, req.optInt("quality", 80), bos)
        else img.compress(Bitmap.CompressFormat.PNG, 100, bos)
        r.put("w", img.width).put("h", img.height).put("format", if (fmt == "jpg") "jpeg" else fmt)
        return bos.toByteArray()
    }

    /** Runs a shell command as the shell user, on the device: no adb round trip. */
    private fun shell(cmd: String, timeoutMs: Int, r: Json) {
        val p = ProcessBuilder("sh", "-c", cmd).redirectErrorStream(true).start()
        val outBytes = ByteArrayOutputStream()
        val reader = Thread { p.inputStream.copyTo(outBytes) }.apply { start() }
        val done = p.waitFor(timeoutMs.toLong(), TimeUnit.MILLISECONDS)
        if (!done) p.destroyForcibly()
        reader.join(1000)
        r.put("code", if (done) p.exitValue() else -1).put("out", outBytes.toString(Charsets.UTF_8.name()))
        if (!done) r.put("timedOut", true)
    }

    private fun displaySize(): Point {
        return try {
            val wm = Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null)
            val p = Point()
            wm.javaClass.getMethod("getBaseDisplaySize", Integer.TYPE, Point::class.java).invoke(wm, 0, p)
            val rot = ua.rotation()
            if (rot == 1 || rot == 3) Point(p.y, p.x) else p
        } catch (_: Throwable) {
            val m = android.content.res.Resources.getSystem().displayMetrics
            Point(m.widthPixels, m.heightPixels)
        }
    }

    private fun UiAutomation.rotation(): Int = try {
        val wm = Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null)
        (wm.javaClass.getMethod("getDefaultDisplayRotation").invoke(wm) as Int)
    } catch (_: Throwable) { 0 }

    companion object {
        const val IDLE_EXIT_MS = 30 * 60_000L
        const val RECENT_REQUESTS = 64
    }
}
