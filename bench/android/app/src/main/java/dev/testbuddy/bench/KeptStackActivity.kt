package dev.testbuddy.bench

import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.widget.FrameLayout
import android.widget.LinearLayout
import com.google.android.material.floatingactionbutton.FloatingActionButton

/**
 * Android's Native screen, "Kept stack" (issue #320): a stack that keeps its previous screen
 * mounted under the one pushed on top, as a JavaScript stack navigator does. In one frame, the
 * pushed screen is the frame's first child, raised by translationZ (drawn last, on top), and
 * the previous screen its second child, a little lower (so Android lists it after the pushed
 * one), drawn under it. Each is sparse (a title, a line, a button) on a solid background of its
 * own: what hides the previous screen is that background, not how much the pushed one holds.
 *
 * The pushed screen: `tk_stack_top_title` "Pushed screen", `tk_stack_status` "Opened N times",
 * `tk_stack_pop` "Pop" (hides it). The previous screen: `tk_stack_prev_title` "Previous
 * screen", "Placeholder line", `tk_stack_prev_open` "Open again" (shows the pushed screen
 * again, N + 1).
 */
class KeptStackActivity : ScenarioActivity() {
    private var opened = 1

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Kept stack")
        val frame = content.put(FrameLayout(this), h = 0, weight = 1f)
        val top = page(frame, R.id.tk_stack_top_title, "Pushed screen")
        top.translationZ = dp(8).toFloat()
        val status = top.text(R.id.tk_stack_status, "Opened $opened times")
        top.button(R.id.tk_stack_pop, "Pop", w = MATCH) { top.visibility = View.GONE }
        val prev = page(frame, R.id.tk_stack_prev_title, "Previous screen")
        prev.translationY = dp(12).toFloat()
        prev.text(text = "Placeholder line", secondary = true)
        prev.button(R.id.tk_stack_prev_open, "Open again", w = MATCH) {
            opened++
            status.text = "Opened $opened times"
            top.visibility = View.VISIBLE
        }
    }

    /** A full-size screen in [frame]: a solid background and a title, nothing else. */
    private fun page(frame: FrameLayout, titleId: Int, title: String): LinearLayout {
        val screen = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(color(R.color.background))
        }
        frame.addView(screen, FrameLayout.LayoutParams(MATCH, MATCH))
        screen.text(titleId, title, size = 20f, bold = true)
        return screen
    }
}

/**
 * Android's Native screen, "See-through overlay" (issue #320 review): content under a
 * transparent overlay laid over all of it, as React Native lays a portal, a dialog's host or a
 * map's controls. The overlay is a full-size frame drawn over the content (raised by
 * translationZ), holding a safe-area frame inset from its top, holding a floating button: the
 * safe area fills nearly all of the content, and the content shows through it and takes taps.
 *
 * The content: `tk_seethrough_status` "Content taps: N", `tk_seethrough_action` "Content
 * action" (N + 1), the line "Content line". The overlay's floating button
 * `tk_seethrough_fab` sets `tk_seethrough_fab_status` "Floating taps: M".
 */
class SeeThroughOverlayActivity : ScenarioActivity() {
    private var taps = 0
    private var fabTaps = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("See-through overlay")
        val frame = content.put(FrameLayout(this), h = 0, weight = 1f)
        val body = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        frame.addView(body, FrameLayout.LayoutParams(MATCH, MATCH))
        val status = body.text(R.id.tk_seethrough_status, "Content taps: 0")
        val fabStatus = body.text(R.id.tk_seethrough_fab_status, "Floating taps: 0")
        body.button(R.id.tk_seethrough_action, "Content action", w = MATCH) {
            taps++
            status.text = "Content taps: $taps"
        }
        body.text(text = "Content line", secondary = true)

        val overlay = FrameLayout(this).apply { translationZ = dp(8).toFloat() }
        frame.addView(overlay, FrameLayout.LayoutParams(MATCH, MATCH))
        val safeArea = FrameLayout(this)
        overlay.addView(safeArea, FrameLayout.LayoutParams(MATCH, MATCH).apply { topMargin = dp(24) })
        val fab = FloatingActionButton(this).apply {
            id = R.id.tk_seethrough_fab
            contentDescription = "Floating action"
            setImageResource(android.R.drawable.ic_input_add)
            setOnClickListener {
                fabTaps++
                fabStatus.text = "Floating taps: $fabTaps"
            }
        }
        safeArea.addView(fab, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.BOTTOM or Gravity.END).apply {
            setMargins(dp(16), dp(16), dp(16), dp(16))
        })
    }
}
