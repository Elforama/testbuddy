package dev.testbuddy.bench

import android.os.Bundle
import android.view.View
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView

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
