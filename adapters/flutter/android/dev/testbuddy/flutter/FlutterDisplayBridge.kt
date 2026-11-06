package dev.testbuddy.flutter

import android.util.Log
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityManager
import io.flutter.embedding.android.FlutterView

/** Optional test-build hook for a host's onFlutterUiDisplayed callback. The agent already
 * wakes on content-change events, so this works with its existing event transport. */
object FlutterDisplayBridge {
    fun displayed(view: FlutterView) {
        // Allow Flutter's renderer listeners to finish updating this view's first-frame flag.
        view.post {
            if (!view.isAttachedToWindow || !view.isShown || !view.hasRenderedFirstFrame()) return@post
            val manager = view.context.getSystemService(AccessibilityManager::class.java)
            if (!manager.isEnabled) return@post
            val event = AccessibilityEvent.obtain(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED)
            event.contentChangeTypes = AccessibilityEvent.CONTENT_CHANGE_TYPE_SUBTREE
            view.sendAccessibilityEventUnchecked(event)
            if (Log.isLoggable("TBFlutter", Log.DEBUG)) Log.d("TBFlutter", "first frame displayed for ${view.context.packageName}")
        }
    }
}
