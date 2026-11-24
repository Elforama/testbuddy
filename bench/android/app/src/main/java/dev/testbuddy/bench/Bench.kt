package dev.testbuddy.bench

import android.animation.TimeAnimator
import android.os.Build
import android.view.View
import android.view.WindowInsets

object AppState {
    var cart = 0
    var onboardingSeen = false
    val askedPermissions = mutableSetOf<String>()
}

const val EXTRA_USERNAME = "username"
const val EXTRA_ITEM = "item"

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

fun forever(frame: (Long) -> Unit): TimeAnimator =
    TimeAnimator().apply {
        setTimeListener { _, total, _ -> frame(total) }
        start()
    }
