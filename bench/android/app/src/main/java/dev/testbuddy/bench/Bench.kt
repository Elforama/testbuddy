package dev.testbuddy.bench

import android.animation.TimeAnimator
import android.os.Build
import android.view.View
import android.view.WindowInsets

/** App state lives in memory for the process only; `pm clear` (clearState) resets it. */
object AppState {
    var cart = 0
    /** Onboarding shows once per process (scenario 7). */
    var onboardingSeen = false
    /** Permissions requested in this process, by name (scenario 3: "denied" vs "not asked"). */
    val askedPermissions = mutableSetOf<String>()
}

const val EXTRA_USERNAME = "username"
const val EXTRA_ITEM = "item"

/**
 * Pads this view by the system bars (and display cutout), on top of its own padding.
 * targetSdk 36 is always edge to edge on Android 15+, so content must keep clear of the bars.
 */
fun View.padForSystemBars() {
    val l = paddingLeft
    val t = paddingTop
    val r = paddingRight
    val b = paddingBottom
    setOnApplyWindowInsetsListener { v, insets ->
        if (Build.VERSION.SDK_INT >= 30) {
            val i = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
            v.setPadding(l + i.left, t + i.top, r + i.right, b + i.bottom)
        } else {
            @Suppress("DEPRECATION")
            v.setPadding(
                l + insets.systemWindowInsetLeft, t + insets.systemWindowInsetTop,
                r + insets.systemWindowInsetRight, b + insets.systemWindowInsetBottom,
            )
        }
        insets
    }
    requestApplyInsets()
}

/**
 * An endless animation: calls [frame] every frame with the milliseconds since it started.
 * A TimeAnimator (a ValueAnimator with no duration) never ends, and unlike a repeating
 * ObjectAnimator it keeps running when the device's animator duration scale is 0, which test
 * devices often use. The bench needs screens that are never quiet.
 */
fun forever(frame: (Long) -> Unit): TimeAnimator =
    TimeAnimator().apply {
        setTimeListener { _, total, _ -> frame(total) }
        start()
    }
