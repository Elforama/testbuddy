package dev.testbuddy.agent

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.AccessibilityServiceInfo
import android.accessibilityservice.InputMethod
import android.net.LocalServerSocket
import android.net.LocalSocket
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.system.Os
import android.system.OsConstants
import android.view.InputDevice
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.inputmethod.EditorInfo
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import org.json.JSONObject
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * Jetpack Compose only sends accessibility
 * events while an accessibility service is enabled (a UiAutomation client is not counted),
 * so without one, Compose screens change silently and an event-driven runner cannot see
 * them. On API 33+ this service also supplies an editor connection for soft Backspace
 * events, without replacing the user's keyboard. The host enables it during a run and
 * removes it afterwards. The historical component name is retained for compatibility.
 */
class NoopService : AccessibilityService() {
    private val handler = Handler(Looper.getMainLooper())
    private var listener: LocalServerSocket? = null
    private var worker: Thread? = null
    @Volatile private var client: LocalSocket? = null
    @Volatile private var stopping = false
    private var sessionTarget: EditorTarget? = null
    private val keys = EditorKeys(
        { handler.postDelayed(it, 1000) }, { handler.removeCallbacks(it) },
        { sendBackspace(it) }, { focusedTarget() == it },
    )

    override fun onServiceConnected() {
        if (Build.VERSION.SDK_INT < 33 || listener != null || stopping) return
        val info = serviceInfo
        info.flags = info.flags or AccessibilityServiceInfo.FLAG_INPUT_METHOD_EDITOR or
            AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS
        serviceInfo = info
        val server = LocalServerSocket(EDITOR_SOCKET)
        listener = server
        Thread({
            while (!stopping) {
                val socket = try { server.accept() } catch (_: java.io.IOException) { break }
                if (stopping) { socket.close(); break }
                try {
                    val uid = socket.peerCredentials.uid
                    if (uid != 0 && uid != 2000) continue
                    client = socket
                    socket.soTimeout = 2500
                    val line = socket.inputStream.bufferedReader().readLine() ?: continue
                    val r = JSONObject(line)
                    val target = EditorTarget(r.getString("pkg"), r.getInt("window"), r.getInt("node"), r.getString("id"), r.getString("cls"))
                    val latch = CountDownLatch(1)
                    var result = "error"
                    handler.post {
                        if (stopping) { result = "rejected"; latch.countDown() }
                        else try {
                            keys.request(target) { result = it; latch.countDown() }
                        } catch (_: Throwable) { result = "error"; latch.countDown() }
                    }
                    // Timeout is not a retry: the client treats a missing reply as
                    // uncertain. Its original request may still finish on main.
                    if (!latch.await(2, TimeUnit.SECONDS)) continue
                    socket.outputStream.write((result + "\n").toByteArray(Charsets.UTF_8))
                    socket.outputStream.flush()
                } catch (t: Throwable) {
                    // Close without "unavailable" after an uncertain request.
                    if (!stopping) android.util.Log.w(TAG, "editor request failed", t)
                } finally {
                    try { socket.close() } catch (_: java.io.IOException) {}
                    client = null
                }
            }
        }, "tb-editor").apply { isDaemon = true; worker = this; start() }
    }

    // The framework invokes this callback only on API 33+ after we request the
    // editor flag above. Older systems never instantiate this type.
    @android.annotation.TargetApi(33)
    override fun onCreateInputMethod(): InputMethod = object : InputMethod(this) {
        override fun onStartInput(attribute: EditorInfo, restarting: Boolean) {
            sessionTarget = focusedTarget()
            keys.changed()
        }
        override fun onFinishInput() {
            // The base implementation clears composing text. Observation alone
            // must not edit anything in the user's keyboard/editor.
            sessionTarget = null
            keys.changed()
        }
        override fun onUpdateSelection(a: Int, b: Int, c: Int, d: Int, e: Int, f: Int) {
            keys.changed()
        }
    }

    private fun focusedTarget(): EditorTarget? {
        if (Build.VERSION.SDK_INT >= 33) clearCache()
        val n = findFocus(AccessibilityNodeInfo.FOCUS_INPUT) ?: return null
        return if (n.isEditable && n.isFocused) editorTarget(n) else null
    }

    private fun sendBackspace(target: EditorTarget): EditorAttempt {
        if (focusedTarget() != target) return EditorAttempt.REJECTED
        if (Build.VERSION.SDK_INT < 33 || sessionTarget != target) return EditorAttempt.WAIT
        val method = inputMethod ?: return EditorAttempt.WAIT
        if (!method.currentInputStarted || method.currentInputEditorInfo?.packageName != target.pkg) return EditorAttempt.WAIT
        val connection = method.currentInputConnection ?: return EditorAttempt.WAIT
        val now = SystemClock.uptimeMillis()
        val flags = KeyEvent.FLAG_SOFT_KEYBOARD or KeyEvent.FLAG_KEEP_TOUCH_MODE
        connection.sendKeyEvent(KeyEvent(now, now, KeyEvent.ACTION_DOWN, KeyEvent.KEYCODE_DEL, 0, 0,
            KeyCharacterMap.VIRTUAL_KEYBOARD, 0, flags, InputDevice.SOURCE_KEYBOARD))
        connection.sendKeyEvent(KeyEvent(now, SystemClock.uptimeMillis(), KeyEvent.ACTION_UP, KeyEvent.KEYCODE_DEL, 0, 0,
            KeyCharacterMap.VIRTUAL_KEYBOARD, 0, flags, InputDevice.SOURCE_KEYBOARD))
        return EditorAttempt.SENT
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) { keys.changed() }
    override fun onInterrupt() { keys.close() }
    override fun onDestroy() {
        stopping = true
        keys.close()
        try { client?.close() } catch (_: java.io.IOException) {}
        worker?.interrupt() // wakes a Java wait; shutdown below wakes native accept
        listener?.let { server ->
            try {
                releaseEditorListener(
                    { Os.shutdown(server.fileDescriptor, OsConstants.SHUT_RDWR) },
                    { server.close() },
                    { worker?.join(1000) },
                )
            } catch (_: java.io.IOException) {}
        }
        super.onDestroy()
    }
}
