package dev.testbuddy.bench

import android.annotation.SuppressLint
import android.os.Bundle
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.accessibility.AccessibilityNodeInfo
import android.widget.FrameLayout
import android.widget.LinearLayout
import androidx.appcompat.widget.AppCompatTextView

/**
 * Android's Native screen, "Slide drawer": a slide-type navigation drawer, as React
 * Navigation's: opening it slides the whole screen, the toggle `drawer_toggle` included, right
 * by the drawer's width, uncovering the drawer under it. The toggle takes its tap in a touch
 * handler and reports no click (as React Native's Pressable and Compose's clickable do), and
 * the slide is a translation, which sends no accessibility event. A tap on it sent twice opens
 * the drawer and closes it again (issue #321). `drawer_accounts` and `drawer_budget` in the
 * drawer show their screen in `drawer_screen` and close it. Two more toggles do the same: one
 * with no view id whose node gets the resource name `drawer-toggle-testid` from an
 * accessibility delegate (as React Native's testID does), and "Open menu", with no id at all.
 */
class SlideDrawerActivity : ScenarioActivity() {
    private var open = false
    private var drawerWidth = 0

    @SuppressLint("ClickableViewAccessibility")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Slide drawer")
        drawerWidth = resources.displayMetrics.widthPixels * 7 / 10
        // The screen hides the drawer under it until it slides away, and takes the touches
        // there: none goes through to the drawer while it is closed.
        column.setBackgroundColor(color(R.color.background))
        column.setOnTouchListener { _, _ -> true }
        val screenName = content.text(R.id.drawer_screen, "Screen: Home", size = 18f, bold = true)

        val drawer = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(color(R.color.surface))
            setPadding(dp(16), dp(96), dp(16), dp(16))
        }
        drawer.text(text = "Menu", size = 20f, bold = true)
        drawer.button(R.id.drawer_accounts, "Accounts", w = MATCH) {
            screenName.text = "Screen: Accounts"
            slide(false)
        }
        drawer.button(R.id.drawer_budget, "Budget", w = MATCH) {
            screenName.text = "Screen: Budget"
            slide(false)
        }
        root.addView(drawer, 0, FrameLayout.LayoutParams(drawerWidth, MATCH))

        content.put(toggle("☰").apply {
            id = R.id.drawer_toggle
            contentDescription = "Show navigation menu"
        }, w = dp(64), h = dp(64))
        // As React Native's testID: no view id of its own (View.NO_ID); an accessibility delegate
        // gives its node the resource name "drawer-toggle-testid", which no resource holds.
        content.put(toggle("≡").apply {
            contentDescription = "Navigation menu"
            accessibilityDelegate = object : View.AccessibilityDelegate() {
                override fun onInitializeAccessibilityNodeInfo(host: View, info: AccessibilityNodeInfo) {
                    super.onInitializeAccessibilityNodeInfo(host, info)
                    info.viewIdResourceName = "drawer-toggle-testid"
                }
            }
        }, w = dp(64), h = dp(64))
        // No id at all: found by its text.
        content.put(toggle("Open menu"), w = dp(96), h = dp(48)) // whole on screen with the drawer open
    }

    /** A toggle that takes its tap in a touch handler, on the lift: no performClick, so no click is reported. */
    @SuppressLint("ClickableViewAccessibility")
    private fun toggle(label: String) = AppCompatTextView(this).apply {
        text = label
        textSize = if (label.length > 1) 14f else 24f
        gravity = Gravity.CENTER
        setTextColor(color(R.color.text))
        isClickable = true
        setOnTouchListener { v, e ->
            if (e.actionMasked == MotionEvent.ACTION_UP && e.x >= 0 && e.y >= 0 && e.x < v.width && e.y < v.height) {
                slide(!open)
            }
            true
        }
    }

    private fun slide(toOpen: Boolean) {
        open = toOpen
        column.animate().translationX(if (toOpen) drawerWidth.toFloat() else 0f).setDuration(SLIDE_MS).start()
    }

    private companion object {
        const val SLIDE_MS = 250L
    }
}
