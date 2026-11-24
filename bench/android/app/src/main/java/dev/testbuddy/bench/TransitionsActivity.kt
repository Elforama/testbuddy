package dev.testbuddy.bench

import android.annotation.SuppressLint
import android.content.Context
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.accessibility.AccessibilityEvent
import android.widget.FrameLayout
import android.widget.LinearLayout
import androidx.appcompat.widget.AppCompatTextView

class TransitionsActivity : ScenarioActivity() {
    private var account = "none"
    private var amount = ""

    @SuppressLint("ClickableViewAccessibility")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.TRANSITIONS.title)

        val body = ClosingFrame(this)
        val at = column.indexOfChild(content)
        column.removeView(content)
        body.addView(content, FrameLayout.LayoutParams(MATCH, MATCH))
        column.addView(body, at, LinearLayout.LayoutParams(MATCH, 0, 1f))

        val accountText = content.text(R.id.tr_account, "To: none", size = 18f, bold = true)
        val summary = content.text(R.id.tr_summary, "")
        fun update() {
            accountText.text = "To: $account"
            summary.text = "Send ${amount.ifEmpty { "nothing" }} to $account"
        }
        update()

        val list = LinearLayout(this).apply {
            id = R.id.tr_list
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(color(R.color.background))
            visibility = View.GONE
            setOnTouchListener { _, _ -> true }
        }
        val rows = mutableListOf<Pair<View, String>>()

        lateinit var remove: Runnable

        fun restore(shown: Boolean) {
            handler.removeCallbacks(remove)
            body.closing = null
            if (list.parent !== body) {
                body.overlay.remove(list)
                body.addView(list)
            }
            list.visibility = if (shown) View.VISIBLE else View.GONE
        }

        fun pick(name: String) {
            account = name
            update()
            if (list.parent === body) body.overlay.add(list)
            body.closing = list
            handler.removeCallbacks(remove)
            handler.postDelayed(remove, CLOSE_MS)
        }

        remove = Runnable { restore(shown = false) }

        body.onClosingTap = { x, y ->
            val lx = x - list.left
            val ly = y - list.top
            rows.firstOrNull { (row, _) -> lx >= row.left && lx < row.right && ly >= row.top && ly < row.bottom }
                ?.let { (_, name) -> pick(name) }
        }

        content.button(R.id.tr_pick, "Choose account") { restore(shown = true) }
        content.heading("Amount")
        content.field(R.id.tr_amount, "Amount", InputType.TYPE_CLASS_NUMBER) {
            amount = it
            update()
        }.isSingleLine = true

        val specs = listOf(
            Triple(R.id.tr_account_checking, "Checking", dp(64)),
            Triple(R.id.tr_account_savings, "Savings", dp(64)),
            Triple(R.id.tr_account_credit, "Credit card", 0),
        )
        for ((id, name, height) in specs) {
            val fills = height == 0
            val b = AppCompatTextView(this).apply {
                this.id = id
                text = name
                textSize = 18f
                setTextColor(color(R.color.text))
                gravity = Gravity.START or (if (fills) Gravity.TOP else Gravity.CENTER_VERTICAL)
                setPadding(dp(20), if (fills) dp(20) else 0, dp(20), 0)
                setOnClickListener { v ->
                    v.sendAccessibilityEvent(AccessibilityEvent.TYPE_VIEW_CLICKED)
                    pick(name)
                }
            }
            list.addView(b, LinearLayout.LayoutParams(MATCH, height, if (fills) 1f else 0f))
            rows += b to name
            if (!fills) {
                list.addView(View(this).apply { setBackgroundColor(color(R.color.divider)) },
                    LinearLayout.LayoutParams(MATCH, dp(1)))
            }
        }
        body.addView(list, FrameLayout.LayoutParams(MATCH, MATCH))
    }

    private class ClosingFrame(context: Context) : FrameLayout(context) {
        var closing: View? = null
        var onClosingTap: (Int, Int) -> Unit = { _, _ -> }
        private var captured = false

        private fun onClosing(ev: MotionEvent): Boolean {
            val v = closing ?: return false
            val x = ev.x.toInt()
            val y = ev.y.toInt()
            return x >= v.left && x < v.right && y >= v.top && y < v.bottom
        }

        override fun onInterceptTouchEvent(ev: MotionEvent): Boolean {
            if (ev.actionMasked == MotionEvent.ACTION_DOWN && onClosing(ev)) {
                captured = true
                return true
            }
            return false
        }

        @SuppressLint("ClickableViewAccessibility")
        override fun onTouchEvent(ev: MotionEvent): Boolean {
            if (!captured) return super.onTouchEvent(ev)
            when (ev.actionMasked) {
                MotionEvent.ACTION_UP -> {
                    captured = false
                    onClosingTap(ev.x.toInt(), ev.y.toInt())
                }
                MotionEvent.ACTION_CANCEL -> captured = false
            }
            return true
        }
    }

    companion object {
        const val CLOSE_MS = 800L
    }
}
