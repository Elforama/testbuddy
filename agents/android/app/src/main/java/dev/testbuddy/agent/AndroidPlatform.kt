package dev.testbuddy.agent

import android.accessibilityservice.AccessibilityServiceInfo
import android.app.UiAutomation
import android.graphics.Bitmap
import android.graphics.Point
import android.graphics.Rect
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.util.Log
import android.view.InputDevice
import android.view.InputEvent
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import java.io.ByteArrayOutputStream
import java.lang.reflect.Method
import kotlin.system.exitProcess

/*
 * Platform.kt's interfaces on a device: UiAutomation (connected as the shell user, Main.kt),
 * the input manager, and the agent's own threads. Everything here needs a device; the logic
 * that uses it is tested on the JVM against fakes.
 */

/** The uptime clock, and timers on the "tb-events" thread. */
class AndroidClock : Clock {
    private val thread = HandlerThread("tb-events").apply { start() }
    private val handler = Handler(thread.looper)

    override fun uptimeMillis(): Long = SystemClock.uptimeMillis()
    override fun nanoTime(): Long = System.nanoTime()
    override fun sleep(ms: Long) = Thread.sleep(ms)
    override fun await(lock: Object, ms: Long) = lock.wait(ms)
    override fun postDelayed(task: Runnable, delayMs: Long) { handler.postDelayed(task, delayMs) }
    override fun postAt(task: Runnable, uptimeMs: Long) { handler.postAtTime(task, uptimeMs) }
    override fun cancel(task: Runnable) = handler.removeCallbacks(task)
}

/** UiAutomation's events and windows, for [Events]. */
class AndroidUiProbe(private val ua: UiAutomation) : UiProbe {
    private val sourceIdMethod = try {
        AccessibilityEvent::class.java.getMethod("getSourceNodeId")
    } catch (_: Throwable) { null }

    /** Sends UiAutomation's accessibility events to [events]. */
    fun listen(events: Events) {
        ua.setOnAccessibilityEventListener { e -> events.onEvent(read(e)) }
    }

    private fun read(e: AccessibilityEvent): UiEvent {
        val toast = e.eventType == AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED
        return UiEvent(
            type = e.eventType,
            pkg = e.packageName?.toString(),
            className = e.className?.toString(),
            contentChangeTypes = e.contentChangeTypes,
            source = sourceKey(e),
            texts = if (toast) e.text.mapNotNull { it?.toString() } else emptyList(),
            eventTime = e.eventTime,
            windowId = e.windowId,
        )
    }

    /** The source view: its window id and node id, combined. Hidden API (getSourceNodeId). */
    private fun sourceKey(e: AccessibilityEvent): Long? {
        val id = try { sourceIdMethod?.invoke(e) as? Long } catch (_: Throwable) { null } ?: return null
        if (id == -1L || id == Long.MAX_VALUE) return null
        return (e.windowId.toLong() shl 48) xor id
    }

    override fun windowIds(): IntArray? = try { ua.windows.map { it.id }.toIntArray() } catch (_: Throwable) { null }

    override fun activeWindowId(): Int = ua.rootInActiveWindow?.windowId ?: -1
    override fun activePackage(): String? = ua.rootInActiveWindow?.packageName?.toString()
    override fun clearFrameStats(window: Int) { ua.clearWindowContentFrameStats(window) }

    override fun lastFramePresentedNanos(window: Int): Long {
        val stats = ua.getWindowContentFrameStats(window) ?: return 0
        val n = stats.frameCount
        if (n == 0) return 0
        return stats.getFramePresentedTimeNano(n - 1)
    }

    override fun setting(ns: String, key: String): String? = try {
        val p = ProcessBuilder("settings", "get", ns, key).redirectErrorStream(true).start()
        val out = p.inputStream.bufferedReader().readText().trim()
        p.waitFor()
        out.takeIf { it.isNotEmpty() && it != "null" }
    } catch (_: Throwable) { null }
}

/**
 * Injects touches and keys. The shell uid holds INJECT_EVENTS, so events go straight to the
 * input manager (as scrcpy does) instead of through UiAutomation, which syncs input
 * transactions around every DOWN/UP (~30 ms each). Falls back to UiAutomation if the hidden
 * API is missing.
 */
class AndroidInjector(private val ua: UiAutomation) : Injector {
    private val manager: Any?
    private val injectMethod: Method?
    private var useManager: Boolean
    private val syncSwipe = try {
        UiAutomation::class.java.getMethod("syncInputTransactions", java.lang.Boolean.TYPE)
    } catch (_: NoSuchMethodException) { null }

    init {
        var m: Any? = null
        var method: Method? = null
        try {
            val cls = try {
                Class.forName("android.hardware.input.InputManagerGlobal") // Android 14+
            } catch (_: ClassNotFoundException) {
                android.hardware.input.InputManager::class.java
            }
            m = cls.getDeclaredMethod("getInstance").invoke(null)
            method = cls.getMethod("injectInputEvent", InputEvent::class.java, Integer.TYPE)
        } catch (t: Throwable) {
            Log.w(TAG, "InputManager injection unavailable, using UiAutomation: ${cause(t)}")
        }
        manager = m
        injectMethod = method
        useManager = m != null && method != null
    }

    override val path: String get() = if (useManager) "input-manager" else "ui-automation"

    override val density: Float get() = android.content.res.Resources.getSystem().displayMetrics.density

    override fun prepareSwipe() {
        if (!useManager) return // UiAutomation's injection already synchronizes input windows.
        val method = syncSwipe ?: return // Older platforms retain their existing path.
        try {
            method.invoke(ua, true)
        } catch (t: Throwable) {
            Log.w(TAG, "input-window synchronization failed; using UiAutomation injection", cause(t))
            useManager = false
        }
    }

    /** False when the platform reported the event not injected (with [sync]: not delivered). */
    private fun inject(e: InputEvent, sync: Boolean): Boolean {
        if (useManager) {
            try {
                val mode = if (sync) 2 /* WAIT_FOR_FINISH */ else 0 /* ASYNC */
                return injectMethod!!.invoke(manager, e, mode) as? Boolean ?: true
            } catch (t: Throwable) {
                Log.w(TAG, "InputManager injection failed, switching to UiAutomation: ${cause(t)}")
                useManager = false
            }
        }
        return ua.injectInputEvent(e, sync)
    }

    // A finger, as a real touch is (and `input tap` sends): the short MotionEvent.obtain leaves
    // the pointer's tool type unknown, and apps that tell touch from mouse by it (Android's
    // Files app: a long press there selects only for a finger) ignore such a press.
    private val finger = arrayOf(MotionEvent.PointerProperties().apply { id = 0; toolType = MotionEvent.TOOL_TYPE_FINGER })
    private val coords = arrayOf(MotionEvent.PointerCoords())

    override fun touch(t: Touch, sync: Boolean): Boolean {
        coords[0].apply { x = t.x; y = t.y; pressure = 1f; size = 1f }
        val e = MotionEvent.obtain(t.downAt, t.at, t.action, 1, finger, coords, 0, 0, 1f, 1f, 0, 0,
            InputDevice.SOURCE_TOUCHSCREEN, 0)
        val ok = inject(e, sync)
        e.recycle()
        return ok
    }

    override fun key(k: Key, sync: Boolean) {
        inject(KeyEvent(k.downAt, k.at, k.action, k.code, k.repeat, k.meta, k.deviceId, k.scanCode, k.flags, InputDevice.SOURCE_KEYBOARD), sync)
    }

    private val keyMap by lazy { KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD) }

    override fun keysFor(ch: Char): List<Key>? = keyMap.getEvents(charArrayOf(ch))?.map {
        Key(it.action, it.keyCode, it.downTime, it.eventTime, it.repeatCount, it.metaState, it.deviceId, it.scanCode, it.flags)
    }
}

/** UiAutomation's windows and nodes, for [Tree]. */
class AndroidNodes(private val ua: UiAutomation) : Nodes {
    init {
        val info = ua.serviceInfo
        info.flags = info.flags or
            AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS or
            AccessibilityServiceInfo.FLAG_REPORT_VIEW_IDS or
            AccessibilityServiceInfo.FLAG_INCLUDE_NOT_IMPORTANT_VIEWS
        ua.serviceInfo = info
    }

    override fun windows(prefetchChildren: Boolean): List<UiWindow> = try { ua.windows.map { AndroidWindow(it, prefetchChildren) } } catch (_: Throwable) { emptyList() }

    override fun activeRoot(prefetchChildren: Boolean): UiNode? = ua.rootInActiveWindow?.let { AndroidNode(it, prefetchChildren) }

    override fun clearCache() {
        if (Build.VERSION.SDK_INT >= 34) ua.clearCache()
        else ua.serviceInfo = ua.serviceInfo
    }

    override fun focusedInput(): Field? {
        // Compose can omit accessibility events. Ask the app for the current input node,
        // rather than trusting a cached field or serializing the whole hierarchy.
        clearCache()
        return ua.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)?.let { AndroidField(it) }
    }
}

private fun Rect.box() = Box(left, top, right, bottom)

class AndroidWindow(private val w: AccessibilityWindowInfo, private val prefetchChildren: Boolean = false) : UiWindow {
    override val id: Int get() = w.id
    override val type: Int get() = w.type
    override val layer: Int get() = w.layer
    override val active: Boolean get() = w.isActive
    override val focused: Boolean get() = w.isFocused
    override val title: CharSequence? get() = w.title
    override val bounds: Box get() = Rect().also { w.getBoundsInScreen(it) }.box()

    override fun root(): UiNode? {
        val n = if (Build.VERSION.SDK_INT >= 33) {
            w.getRoot(
                AccessibilityNodeInfo.FLAG_PREFETCH_DESCENDANTS_HYBRID or
                    AccessibilityNodeInfo.FLAG_PREFETCH_UNINTERRUPTIBLE,
            )
        } else w.root
        return n?.let { AndroidNode(it, prefetchChildren) }
    }
}

class AndroidNode(private val n: AccessibilityNodeInfo, private val prefetchChildren: Boolean = false) : UiNode {
    override val bounds: Box get() = Rect().also { n.getBoundsInScreen(it) }.box()
    override val className: CharSequence? get() = n.className
    override val packageName: CharSequence? get() = n.packageName
    override val windowId: Int get() = n.windowId
    override val windowType: Int get() = try { n.window?.type ?: -1 } catch (_: Throwable) { -1 }
    override val text: CharSequence? get() = n.text
    override val contentDescription: CharSequence? get() = n.contentDescription
    override val viewId: String? get() = n.viewIdResourceName
    override val hint: CharSequence? get() = n.hintText
    override val error: CharSequence? get() = n.error
    override val stateDescription: CharSequence? get() = if (Build.VERSION.SDK_INT >= 30) n.stateDescription else null
    override val clickable: Boolean get() = n.isClickable
    override val enabled: Boolean get() = n.isEnabled
    override val focused: Boolean get() = n.isFocused
    override val scrollable: Boolean get() = n.isScrollable
    override val checked: Boolean get() = n.isChecked
    override val selected: Boolean get() = n.isSelected
    override val checkable: Boolean get() = n.isCheckable
    override val password: Boolean get() = n.isPassword
    override val editable: Boolean get() = n.isEditable
    override val longClickable: Boolean get() = n.isLongClickable
    override val focusable: Boolean get() = n.isFocusable
    override val showingHint: Boolean get() = n.isShowingHintText
    override val visibleToUser: Boolean get() = n.isVisibleToUser
    override val canScrollForward: Boolean get() = n.actionList.any {
        it.id == AccessibilityNodeInfo.ACTION_SCROLL_FORWARD ||
            it.id == AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_DOWN.id ||
            it.id == AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_RIGHT.id
    }
    override val canScrollBackward: Boolean get() = n.actionList.any {
        it.id == AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD ||
            it.id == AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_UP.id ||
            it.id == AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_LEFT.id
    }
    override val collection: IntArray? get() = n.collectionInfo?.let { intArrayOf(it.rowCount, it.columnCount) }
    override val collectionItem: IntArray? get() = n.collectionItemInfo?.let { intArrayOf(it.rowIndex, it.columnIndex) }
    override val drawingOrder: Int get() = n.drawingOrder
    override val childCount: Int get() = n.childCount
    override fun child(i: Int): UiNode? {
        val child = if (prefetchChildren && Build.VERSION.SDK_INT >= 33) {
            n.getChild(i, AccessibilityNodeInfo.FLAG_PREFETCH_DESCENDANTS_HYBRID or
                AccessibilityNodeInfo.FLAG_PREFETCH_UNINTERRUPTIBLE)
        } else n.getChild(i)
        return child?.let { AndroidNode(it, prefetchChildren) }
    }
}

class AndroidField(private val n: AccessibilityNodeInfo) : Field {
    override val identity: Any get() = n // AccessibilityNodeInfo equality uses window/source IDs.
    override val editable: Boolean get() = n.isEditable
    override val showingHint: Boolean get() = n.isShowingHintText
    override val nativePlainText: Boolean get() = n.className?.toString() == "android.widget.EditText" && !n.isPassword
    override val text: CharSequence? get() = n.text
    override val selectionStart: Int get() = n.textSelectionStart
    override val selectionEnd: Int get() = n.textSelectionEnd
    override fun editorTarget(): EditorTarget? =
        if (Build.VERSION.SDK_INT >= 33 && n.isEditable && n.isFocused) dev.testbuddy.agent.editorTarget(n) else null

    override fun submitIme(): Boolean {
        if (!canSubmitIme()) return false
        return n.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id)
    }

    override fun canSubmitIme(): Boolean {
        // Multiline Enter must remain a newline; custom/old providers retain
        // ordinary key delivery unless they explicitly advertise this action.
        if (Build.VERSION.SDK_INT < 30 || !n.isEditable || !n.isFocused || n.isMultiLine) return false
        val action = AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id
        return n.actionList.any { it.id == action }
    }

    override val packageName: CharSequence? get() = n.packageName
    override fun setText(text: CharSequence): Boolean {
        val args = Bundle()
        args.putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, text)
        return n.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)
    }

    override fun select(start: Int, end: Int): Boolean {
        val sel = Bundle()
        sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_START_INT, start)
        sel.putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_END_INT, end)
        return n.performAction(AccessibilityNodeInfo.ACTION_SET_SELECTION, sel)
    }
}

/** The display, screenshots, the shell and the process. */
class AndroidDevice(private val ua: UiAutomation) : Device {
    override val sdk: Int get() = Build.VERSION.SDK_INT
    override val density: Double get() = android.content.res.Resources.getSystem().displayMetrics.density.toDouble()

    override fun displaySize(): IntArray {
        return try {
            val wm = Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null)
            val p = Point()
            wm.javaClass.getMethod("getBaseDisplaySize", Integer.TYPE, Point::class.java).invoke(wm, 0, p)
            val rot = rotation()
            if (rot == 1 || rot == 3) intArrayOf(p.y, p.x) else intArrayOf(p.x, p.y)
        } catch (_: Throwable) {
            val m = android.content.res.Resources.getSystem().displayMetrics
            intArrayOf(m.widthPixels, m.heightPixels)
        }
    }

    override fun rotation(): Int = try {
        val wm = Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null)
        (wm.javaClass.getMethod("getDefaultDisplayRotation").invoke(wm) as Int)
    } catch (_: Throwable) { 0 }

    override fun rotate(to: Rotation) {
        ua.setRotation(when (to) {
            Rotation.LANDSCAPE_LEFT -> UiAutomation.ROTATION_FREEZE_90
            Rotation.LANDSCAPE_RIGHT -> UiAutomation.ROTATION_FREEZE_270
            Rotation.UPSIDE_DOWN -> UiAutomation.ROTATION_FREEZE_180
            Rotation.PORTRAIT -> UiAutomation.ROTATION_FREEZE_0
            Rotation.FREE -> UiAutomation.ROTATION_UNFREEZE
        })
    }

    override fun foregroundPackage(): String? = ua.rootInActiveWindow?.packageName?.toString()

    override fun screenshot(scale: Double, format: String, quality: Int): Shot {
        val bmp = ua.takeScreenshot() ?: throw IllegalStateException("screenshot failed")
        val img = if (scale in 0.05..0.99) {
            Bitmap.createScaledBitmap(bmp, (bmp.width * scale).toInt().coerceAtLeast(1), (bmp.height * scale).toInt().coerceAtLeast(1), true)
        } else bmp
        val bos = ByteArrayOutputStream(256 * 1024)
        if (format == "jpeg") img.compress(Bitmap.CompressFormat.JPEG, quality, bos)
        else img.compress(Bitmap.CompressFormat.PNG, 100, bos)
        return Shot(img.width, img.height, format, bos.toByteArray())
    }

    /** Runs a shell command as the shell user, on the device: no adb round trip ([Shell]). */
    override fun shell(cmd: String, timeoutMs: Int): ShellResult = Shell.run(cmd, timeoutMs)

    override fun dump(service: String, args: List<String>, timeoutMs: Int): ShellResult? = BinderDump.run(service, args, timeoutMs)

    override fun exitSoon() {
        Thread { disableNoopService(); exitProcess(0) }.start()
    }
}
