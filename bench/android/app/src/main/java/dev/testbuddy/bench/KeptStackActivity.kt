package dev.testbuddy.bench

import android.os.Bundle
import android.view.View
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView

/**
 * Android's Native screen, "Kept stack" (issue #320): a stack that keeps its previous screen
 * mounted under the one pushed on top, as a JavaScript stack navigator does. In one frame, the
 * pushed screen is the frame's first child, raised by translationZ (drawn last, on top), and
 * the previous screen its second child, a little lower (so Android lists it after the pushed
 * one), drawn under it. Both draw a solid background, a title and a button at the same place.
 *
 * The pushed screen: `tk_stack_top_title` "Pushed screen", `tk_stack_status` "Opened N times",
 * `tk_stack_pop` "Pop" (hides it). The previous screen: `tk_stack_prev_title` "Previous
 * screen", `tk_stack_prev_open` "Open again" (shows the pushed screen again, N + 1).
 */
class KeptStackActivity : ScenarioActivity() {
    private var opened = 1

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Kept stack")
        val frame = content.put(FrameLayout(this), h = 0, weight = 1f)
        val (top, topBody) = page(frame, R.id.tk_stack_top_title, "Pushed screen")
        top.translationZ = dp(8).toFloat()
        val status = topBody.text(R.id.tk_stack_status, "Opened $opened times")
        topBody.button(R.id.tk_stack_pop, "Pop", w = MATCH) { top.visibility = View.GONE }
        val (prev, prevBody) = page(frame, R.id.tk_stack_prev_title, "Previous screen")
        prev.translationY = dp(12).toFloat()
        prevBody.text(text = "Placeholder line", secondary = true)
        prevBody.button(R.id.tk_stack_prev_open, "Open again", w = MATCH) {
            opened++
            status.text = "Opened $opened times"
            top.visibility = View.VISIBLE
        }
    }

    /** A full-size screen in [frame]: a solid background, a title, and a scrolling body. */
    private fun page(frame: FrameLayout, titleId: Int, title: String): Pair<View, LinearLayout> {
        val screen = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(color(R.color.background))
        }
        frame.addView(screen, FrameLayout.LayoutParams(MATCH, MATCH))
        screen.text(titleId, title, size = 20f, bold = true)
        val scroll = screen.put(ScrollView(this), h = 0, weight = 1f)
        val body = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        scroll.addView(body, FrameLayout.LayoutParams(MATCH, MATCH))
        return screen to body
    }
}
