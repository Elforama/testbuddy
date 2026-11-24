package dev.testbuddy.agent

import android.app.UiAutomation
import android.os.HandlerThread
import android.os.Looper
import android.util.Log
import kotlin.system.exitProcess

const val AGENT_VERSION = "0.1.67"
const val SOCKET_NAME = "testbuddy-agent"
const val TAG = "TestBuddyAgent"

object Main {
    @JvmStatic
    fun main(args: Array<String>) {
        Log.i(TAG, "starting v$AGENT_VERSION")
        Thread.setDefaultUncaughtExceptionHandler { t, e -> Log.e(TAG, "uncaught on ${t.name}", e) }
        @Suppress("DEPRECATION")
        Looper.prepareMainLooper()

        val ua = try {
            connectUiAutomation()
        } catch (t: Throwable) {
            val msg = "could not connect UiAutomation: another UI automation client (another test runner, uiautomator, an instrumentation test) may be running. ${cause(t)}"
            Log.e(TAG, msg)
            System.err.println(msg)
            exitProcess(3)
        }

        val probe = AndroidUiProbe(ua)
        val clock = AndroidClock()
        val events = Events(probe, clock)
        probe.listen(events)
        val tree = Tree(AndroidNodes(ua))
        tree.openedWindow = { events.openedWindow }
        val device = AndroidDevice(ua)
        val input = Input(AndroidInjector(ua), clock, { EditorSettings.read(device, it) }) { tree.focusedInput() }
        val background = java.util.concurrent.Executors.newSingleThreadExecutor { r -> Thread(r, "tb-location").apply { isDaemon = true } }
        val ops = Ops(device, tree, input, events, clock) { background.execute(it) }
        val server = Server(ops, events)
        Thread({
            try {
                server.serve()
            } catch (t: Throwable) {
                Log.e(TAG, "server crashed", t)
            }
            ops.shutdown()
            try { UiAutomation::class.java.getMethod("disconnect").invoke(ua) } catch (_: Throwable) {}
            exitProcess(1)
        }, "tb-server").start()
        Looper.loop()
    }

    @android.annotation.SuppressLint("BlockedPrivateApi", "SoonBlockedPrivateApi")
    private fun connectUiAutomation(): UiAutomation {
        val thread = HandlerThread("tb-uiautomation").apply { start() }
        val connection = Class.forName("android.app.UiAutomationConnection")
            .getDeclaredConstructor().newInstance()
        val ua = UiAutomation::class.java
            .getConstructor(Looper::class.java, Class.forName("android.app.IUiAutomationConnection"))
            .newInstance(thread.looper, connection)
        val flags = UiAutomation.FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES
        val cls = UiAutomation::class.java
        val connected = try {
            cls.getDeclaredMethod("connectWithTimeout", Integer.TYPE, java.lang.Long.TYPE).apply { isAccessible = true }
                .invoke(ua, flags, 10_000L); "connectWithTimeout(flags)"
        } catch (_: NoSuchMethodException) {
            try {
                cls.getDeclaredMethod("connect", Integer.TYPE).apply { isAccessible = true }.invoke(ua, flags); "connect(flags)"
            } catch (_: NoSuchMethodException) {
                cls.getMethod("connect").invoke(ua); "connect() (suppresses other services)"
            }
        }
        Log.i(TAG, "UiAutomation via $connected")
        return ua
    }
}

fun disableNoopService() {
    try {
        val p = ProcessBuilder("sh", "-c",
            "cur=$(settings get secure enabled_accessibility_services); " +
            "new=$(echo \"${'$'}cur\" | tr ':' '\\n' | grep -v 'dev.testbuddy.agent/' | grep -v '^null$' | paste -sd ':' -); " +
            "if [ -z \"${'$'}new\" ]; then settings delete secure enabled_accessibility_services; " +
            "else settings put secure enabled_accessibility_services \"${'$'}new\"; fi").start()
        p.waitFor()
    } catch (_: Throwable) {}
}

fun cause(t: Throwable): Throwable =
    (t as? java.lang.reflect.InvocationTargetException)?.targetException ?: t
