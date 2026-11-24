package dev.testbuddy.agent

import android.util.Log
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

class Ops(
    private val device: Device,
    private val tree: Tree,
    private val input: Input,
    private val events: Events,
    private val clock: Clock,
    background: (Runnable) -> Unit = { it.run() },
) {
    private val location = Location(device, clock, background)

    class Reply(val text: String, val bin: ByteArray? = null)

    @Volatile var idleLimitMs = Server.ORPHAN_EXIT_MS
        private set

    @Volatile private var lastActionAt = 0L
    @Volatile private var lastTapAt = -1L

    val injectPath: String get() = input.path

    fun hello(): String {
        val size = device.displaySize()
        return Json().obj().put("ev", "hello").put("platform", "android").put("version", AGENT_VERSION)
            .put("sdk", device.sdk).put("w", size[0]).put("h", size[1])
            .put("density", device.density)
            .put("inject", input.path).put("seq", events.seq)
            .put("location", location.isOn).endObj().toString()
    }

    fun handle(line: String, client: Any? = null): Reply? {
        if (line.isBlank()) return null
        val req = try { JSONObject(line) } catch (_: Throwable) { return null }
        return handle(req, client)
    }

    fun clientGone(client: Any) {
        try { location.released(client) } catch (t: Throwable) { Log.w(TAG, "location release", t) }
    }

    fun shutdown() {
        try { location.off() } catch (t: Throwable) { Log.w(TAG, "location off", t) }
    }

    fun handle(req: JSONObject, client: Any? = null): Reply {
        val id = req.optLong("id")
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

    private class Done {
        val latch = CountDownLatch(1)
        @Volatile var reply: String? = null
        @Volatile var bin: ByteArray? = null
    }

    private val recent = object : LinkedHashMap<String, Done>(64, 0.75f, false) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Done>) = size > RECENT_REQUESTS
    }

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
                val timeout = req.optInt("timeout", 3000)
                val sent = tree.keyboardShown()
                if (sent) input.key(android.view.KeyEvent.KEYCODE_BACK)
                var hidden = events.awaitCondition(timeout) { !tree.keyboardShown() }
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

    private fun action(op: String, req: JSONObject, r: Json) {
        val touch = op == "tap" || op == "doubleTap" || op == "press"
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
            "swipe" -> if (req.optLong("hold", 0) > 0) {
                input.holdDrag(fx(req, "x1"), fx(req, "y1"), fx(req, "x2"), fx(req, "y2"), req.getLong("hold"), req.optLong("ms", 1000))
            } else if (req.optBoolean("drag")) {
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
                val ci = req.optBoolean("ci")
                val typed = if (checked) input.typeChecked(text, events, waitForFocus = ci, ci = ci) else input.type(text)
                if (typed == null) {
                    r.put("verifyUnavailable", true).put("seq", events.seq)
                    return
                }
                if (!typed) throw IllegalStateException("no focused text field to type into")
                if (checked) input.lastTyping?.let { r.put("typing", it) }
            }
            "erase" -> {
                input.erase(req.optInt("n", 50), events, ci = req.optBoolean("ci"))
                input.lastErase?.let { r.put("erase", it) }
            }
            "clear" -> r.put("cleared", input.clearFocused())
        }
        val t1 = clock.uptimeMillis()
        lastActionAt = t1
        r.put("injectMs", t1 - t0)
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
        r.put("clicked", events.clicks > clicksBefore).put("clicksBefore", clicksBefore)
        r.put("seq", events.seq)
    }

    @Volatile private var lastInputPackage: String? = null

    private fun packageOf(w: UiWindow): String? = try { w.root()?.packageName?.toString() } catch (_: Throwable) { null }

    private fun comesOver(w: UiWindow): Boolean {
        val seen = events.windows.openedBy(w.id)
        val first = seen.firstOrNull() ?: return false
        val pkg = packageOf(w) ?: first.pkg ?: return false
        val kinds = seen.map { e -> e.cls?.let { device.isActivity(pkg, it) } }
        if (kinds.any { it == null }) return false
        val came = events.windows.firstAppeared(w.id)
        if (came <= 0 || kotlin.math.abs(first.at - came) > OPENED_EVENT_MS) return false
        if (kinds.any { it == true }) {
            return kinds[0] == true && seen.indices.drop(1).none { kinds[it] == true && seen[it].cls != first.cls } &&
                floatsOnTheApp(w, pkg)
        }
        if (viewClass(first.cls!!)) return false
        val under = events.windows.listedBefore(w.id).mapNotNull { events.windows.openedBy(it).firstOrNull()?.pkg }
        return pkg in under || (lastInputPackage != null && lastInputPackage in under)
    }

    private fun floatsOnTheApp(w: UiWindow, pkg: String): Boolean {
        val app = lastInputPackage ?: return false
        if (pkg == app) return false
        val box = w.bounds
        return events.windows.listedBefore(w.id).any { id ->
            val under = events.windows.boundsOf(id)
            events.windows.openedBy(id).firstOrNull()?.pkg == app && under != null && !events.windows.listed(id) &&
                box.left > under.left && box.top > under.top && box.right < under.right && box.bottom < under.bottom
        }
    }

    private fun viewClass(cls: String): Boolean =
        cls.startsWith("android.view.") || cls.startsWith("android.widget.") ||
            cls.endsWith("Layout") || cls.endsWith("View") || cls.endsWith("Pane")

    private fun fx(req: JSONObject, k: String): Float = req.getDouble(k).toFloat()

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

    var inputWindowAgeMs = CI_INPUT_WINDOW_AGE_MS

    companion object {
        const val RECENT_REQUESTS = 64
        const val CI_INPUT_WINDOW_AGE_MS = 400L
        const val FIRST_INPUT_HORIZON_MS = 1_500L
        const val OPENED_EVENT_MS = 1_000L
        const val KEYBOARD_STEADY_MS = 100
        const val TAP_RECENT_MS = 10_000L

        fun withId(reply: String, id: Long): String {
            val comma = reply.indexOf(',')
            return if (comma < 0) reply else "{\"id\":$id" + reply.substring(comma)
        }
    }
}
