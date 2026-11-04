package dev.testbuddy.agent

import android.app.UiAutomation
import android.os.HandlerThread
import android.os.Looper
import android.util.Log
import kotlin.system.exitProcess

const val AGENT_VERSION = "0.1.18"
const val SOCKET_NAME = "testbuddy-agent"
const val TAG = "TestBuddyAgent"

/**
 * The Test Buddy Android agent. The host starts it as the shell user:
 *
 *   adb push agent.apk /data/local/tmp/tb-agent.apk
 *   adb shell CLASSPATH=/data/local/tmp/tb-agent.apk app_process / --nice-name=tb-agent dev.testbuddy.agent.Main
 *
 * It listens on the abstract unix socket "testbuddy-agent" (the host reaches it through
 * `adb forward tcp:N localabstract:testbuddy-agent`) and keeps running between host runs, so
 * only the first run pays the ~1 s process start. It exits after [IDLE_EXIT_MS] with no client.
 *
 * Shell uid gives it INJECT_EVENTS (input straight to the input manager) and a UiAutomation
 * connection (accessibility tree and events), the way `uiautomator dump` gets one.
 */
object Main {
    @JvmStatic
    fun main(args: Array<String>) {
        Log.i(TAG, "starting v$AGENT_VERSION")
        // A failure on a client thread must not take the agent down with it.
        Thread.setDefaultUncaughtExceptionHandler { t, e -> Log.e(TAG, "uncaught on ${t.name}", e) }
        @Suppress("DEPRECATION")
        Looper.prepareMainLooper() // AccessibilityInteractionClient builds a Handler on it

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
        val input = Input(AndroidInjector(ua), clock) { tree.focusedInput() }
        val ops = Ops(AndroidDevice(ua), tree, input, events, clock)
        val server = Server(ops, events)
        Thread({
            try {
                server.serve()
            } catch (t: Throwable) {
                Log.e(TAG, "server crashed", t)
            }
            try { UiAutomation::class.java.getMethod("disconnect").invoke(ua) } catch (_: Throwable) {}
            exitProcess(1)
        }, "tb-server").start()
        Looper.loop()
    }

    /** new UiAutomation(looper, new UiAutomationConnection()).connect(): hidden API. */
    private fun connectUiAutomation(): UiAutomation {
        val thread = HandlerThread("tb-uiautomation").apply { start() }
        val connection = Class.forName("android.app.UiAutomationConnection")
            .getDeclaredConstructor().newInstance()
        val ua = UiAutomation::class.java
            .getConstructor(Looper::class.java, Class.forName("android.app.IUiAutomationConnection"))
            .newInstance(thread.looper, connection)
        // connect(flags) without suppressing other accessibility services: our NoopService
        // (which makes Compose send events) and TalkBack keep running.
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

/** Takes our NoopService out of the enabled accessibility services (on exit). */
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
