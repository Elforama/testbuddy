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

class SlideDrawerActivity : ScenarioActivity() {
    private var open = false
    private var drawerWidth = 0

    @SuppressLint("ClickableViewAccessibility")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Slide drawer")
        drawerWidth = resources.displayMetrics.widthPixels * 7 / 10
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
        content.put(toggle("≡").apply {
            contentDescription = "Navigation menu"
            accessibilityDelegate = object : View.AccessibilityDelegate() {
                override fun onInitializeAccessibilityNodeInfo(host: View, info: AccessibilityNodeInfo) {
                    super.onInitializeAccessibilityNodeInfo(host, info)
                    info.viewIdResourceName = "drawer-toggle-testid"
                }
            }
        }, w = dp(64), h = dp(64))
        content.put(toggle("Open menu"), w = dp(96), h = dp(48))
    }

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
