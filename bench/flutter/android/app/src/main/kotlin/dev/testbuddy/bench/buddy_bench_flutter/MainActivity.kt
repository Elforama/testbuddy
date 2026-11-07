package dev.testbuddy.bench.buddy_bench_flutter

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.graphics.PixelFormat
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.provider.Settings
import android.view.Gravity
import android.view.WindowManager
import android.widget.TextView
import android.content.Intent
import android.content.pm.PackageManager
import dev.testbuddy.bench.ToolkitComposeActivity
import dev.testbuddy.bench.ToolkitViewsActivity
import dev.testbuddy.bench.ToolkitWebViewActivity
import dev.testbuddy.flutter.FlutterDisplayBridge
import io.flutter.embedding.android.FlutterView
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private var bubble: TextView? = null
    private var bubbleReply: MethodChannel.Result? = null
    private var pending: MethodChannel.Result? = null
    private val permissions = mapOf(
        "Camera" to "android.permission.CAMERA", "Location" to "android.permission.ACCESS_FINE_LOCATION",
        "Contacts" to "android.permission.READ_CONTACTS", "Notifications" to "android.permission.POST_NOTIFICATIONS",
        "Microphone" to "android.permission.RECORD_AUDIO"
    )
    override fun onFlutterUiDisplayed() {
        super.onFlutterUiDisplayed()
        findViewById<FlutterView>(FLUTTER_VIEW_ID)?.let(FlutterDisplayBridge::displayed)
    }

    override fun configureFlutterEngine(engine: FlutterEngine) {
        super.configureFlutterEngine(engine)
        MethodChannel(engine.dartExecutor.binaryMessenger, "buddy/host").setMethodCallHandler { call, result ->
            when (call.method) {
                "overlayStatus" -> { result.success(Settings.canDrawOverlays(this)); return@setMethodCallHandler }
                "overlay" -> { showBubble(result); return@setMethodCallHandler }
                "post" -> {
                    val notifications = getSystemService(NotificationManager::class.java)
                    val allowed = notifications.areNotificationsEnabled()
                    if (allowed) {
                        notifications.createNotificationChannel(NotificationChannel("bench","Bench",NotificationManager.IMPORTANCE_DEFAULT))
                        notifications.notify(1, Notification.Builder(this,"bench")
                            .setContentTitle("Bench says hi").setSmallIcon(android.R.drawable.ic_dialog_info).build())
                    }
                    result.success(if(allowed) "Posted" else "Not allowed")
                    return@setMethodCallHandler
                }
            }
            val key = call.arguments as String
            if (call.method == "toolkit") {
                val target = when (key) {
                    "views" -> ToolkitViewsActivity::class.java
                    "compose" -> ToolkitComposeActivity::class.java
                    else -> ToolkitWebViewActivity::class.java
                }
                startActivity(Intent(this, target))
                result.success(null)
            } else {
                val perm = permissions.getValue(key)
                val granted = (key == "Notifications" && Build.VERSION.SDK_INT < 33) || checkSelfPermission(perm) == PackageManager.PERMISSION_GRANTED
                val history = getSharedPreferences("requests", MODE_PRIVATE)
                if (call.method == "status" || granted) {
                    result.success(if (granted) "granted" else if (history.contains(key)) "denied" else "not asked")
                } else if (pending == null) {
                    history.edit().putBoolean(key,true).apply()
                    pending = result
                    requestPermissions(if (key=="Location") arrayOf(perm,"android.permission.ACCESS_COARSE_LOCATION") else arrayOf(perm), 42)
                } else result.error("busy", "A permission request is pending", null)
            }
        }
    }
    private fun showBubble(reply: MethodChannel.Result) {
        if (!Settings.canDrawOverlays(this)) {
            startActivity(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:$packageName")))
            reply.success("Overlay settings")
            return
        }
        if (bubble != null) { reply.success("Bubble open"); return }
        val size = (160 * resources.displayMetrics.density).toInt()
        val params = WindowManager.LayoutParams(size,size,WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE,PixelFormat.TRANSLUCENT).apply {
            gravity = Gravity.BOTTOM or Gravity.END
        }
        val view = TextView(this).apply {
            id = dev.testbuddy.bench.R.id.bubble
            contentDescription = "Bubble"
            text = "Bubble"
            gravity = Gravity.CENTER
            background = GradientDrawable().apply { shape = GradientDrawable.OVAL; setColor(0xff92aaff.toInt()) }
            setOnClickListener { closeBubble() }
        }
        windowManager.addView(view, params)
        bubble = view
        bubbleReply = reply
    }

    private fun closeBubble() {
        bubble?.let { windowManager.removeView(it) }
        bubble = null
        bubbleReply?.success("Bubble closed")
        bubbleReply = null
    }
    override fun onDestroy() { closeBubble(); super.onDestroy() }

    override fun onRequestPermissionsResult(code:Int, permissions:Array<out String>, results:IntArray) {
        super.onRequestPermissionsResult(code,permissions,results)
        if(code == 42) {
            pending?.success(if(results.firstOrNull()==PackageManager.PERMISSION_GRANTED) "granted" else "denied")
            pending=null
        }
    }
}
