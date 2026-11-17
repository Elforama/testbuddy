package dev.testbuddy.bench

import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.TextView
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

/**
 * Android's Native screen, "Restored feed" (issue #284): a feed that turns with the device and
 * reloads its content when it is recreated. Its list (`restore_list`) starts with the line "Top
 * of the feed" (`restore_top`), then "Story 1"…"Story 40". `restore_jump` "Jump to Story 20"
 * puts Story 20 at the top of the list.
 *
 * Recreated (a turn, a fold, split screen), the screen first shows the list empty and a spinner
 * (`restore_spinner`), and loads its rows [RELOAD_MS] later: from the top, its place lost, as an
 * app that opens a new feed on recreation does; or, with `restore_keep` "Keep place: on", at
 * the row that was at the top (its place kept). The setting lives in memory for the process.
 */
class RestoredFeedActivity : ScenarioActivity() {
    private lateinit var layout: LinearLayoutManager

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Restored feed")
        content.row {
            button(R.id.restore_keep, keepLabel()) { v ->
                keepPlace = !keepPlace
                (v as TextView).text = keepLabel()
            }
            button(R.id.restore_jump, "Jump to Story 20") { layout.scrollToPositionWithOffset(20, 0) }
        }
        val frame = content.put(FrameLayout(this), h = 0, weight = 1f)
        layout = LinearLayoutManager(this)
        val rows = Rows(mutableListOf<Int>(), { parent: ViewGroup ->
            TextView(parent.context).apply {
                layoutParams = ViewGroup.LayoutParams(MATCH, dp(56))
                gravity = Gravity.CENTER_VERTICAL
                textSize = 18f
                setTextColor(color(R.color.text))
                setPadding(dp(8), 0, dp(8), 0)
            }
        }) { v, n, _ ->
            v as TextView
            if (n == 0) {
                v.id = R.id.restore_top
                v.text = "Top of the feed"
            } else {
                v.id = View.NO_ID
                v.text = "Story $n"
            }
        }
        val list = RecyclerView(this).apply {
            id = R.id.restore_list
            layoutManager = layout
            adapter = rows
            // The list keeps no state of its own: the screen decides where it reloads.
            isSaveEnabled = false
        }
        frame.addView(list, FrameLayout.LayoutParams(MATCH, MATCH))
        val load = { at: Int, offset: Int ->
            rows.items.addAll(0..40)
            rows.notifyDataSetChanged()
            layout.scrollToPositionWithOffset(at, offset)
        }
        if (savedInstanceState == null) {
            load(0, 0)
            return
        }
        val spinner = spinner(R.id.restore_spinner)
        frame.addView(spinner, FrameLayout.LayoutParams(dp(36), dp(36), Gravity.CENTER))
        val at = savedInstanceState.getInt(KEY_AT)
        val offset = savedInstanceState.getInt(KEY_OFFSET)
        later(RELOAD_MS) {
            frame.removeView(spinner)
            if (keepPlace) load(at, offset) else load(0, 0)
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        val at = layout.findFirstVisibleItemPosition()
        outState.putInt(KEY_AT, maxOf(at, 0))
        outState.putInt(KEY_OFFSET, layout.findViewByPosition(at)?.top ?: 0)
    }

    private fun keepLabel() = if (keepPlace) "Keep place: on" else "Keep place: off"

    private companion object {
        const val RELOAD_MS = 1000L
        const val KEY_AT = "restore_at"
        const val KEY_OFFSET = "restore_offset"
        var keepPlace = false
    }
}
