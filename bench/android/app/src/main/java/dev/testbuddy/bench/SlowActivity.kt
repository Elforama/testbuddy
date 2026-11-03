package dev.testbuddy.bench

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView

/** Scenario 2, "Slow and janky": everything is late, slow or blocks the UI thread. */
class SlowActivity : ScenarioActivity() {
    private var jankTaps = 0
    private var lateTaps = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.SLOW.title, scroll = true)

        // Staged load: 3 skeleton bars for 2 s, then "Loaded" and three rows 400 ms apart.
        content.heading("Staged load")
        val stage = content.column { }
        val skeletons = (1..3).map { n ->
            stage.put(View(this).apply {
                id = idOf("skeleton_$n")
                background = rounded(color(R.color.divider), 6)
            }, w = dp(if (n == 2) 220 else 280), h = dp(20), top = 10)
        }
        later(STAGE_MS) {
            skeletons.forEach { stage.removeView(it) }
            stage.text(R.id.stage_text, "Loaded", size = 18f, bold = true)
            for (n in 1..3) {
                later((n - 1) * ROW_GAP_MS) {
                    val row = stage.text(idOf("slow_row_$n"), "Slow row $n")
                    row.alpha = 0f
                    track(animateFor(FADE_MS, { row.alpha = it }))
                }
            }
        }

        // The late button: nothing reserves its space, so what is below it moves down.
        content.heading("Late")
        val lateSlot = content.column(top = 0) { }
        val lateCount = content.text(R.id.late_count, "Late taps: 0")
        later(LATE_MS) {
            lateSlot.button(R.id.late_button, "Late button") {
                lateTaps++
                lateCount.text = "Late taps: $lateTaps"
            }
        }

        content.heading("Slow push")
        content.button(R.id.slow_open, "Open slowly") {
            later(SLOW_OPEN_DELAY_MS) {
                startActivity(Intent(this, SlowDetailActivity::class.java))
                @Suppress("DEPRECATION")
                overridePendingTransition(0, 0)
            }
        }

        content.heading("Jank")
        val jankText = content.text(R.id.jank_text, "Not blocked")
        content.button(R.id.jank_button, "Block UI") {
            busyWait(JANK_MS)
            jankTaps++
            jankText.text = "Unblocked $jankTaps"
        }

        content.heading("Slow list")
        content.button(R.id.slow_list_open, "Slow list") {
            startActivity(Intent(this, SlowListActivity::class.java))
        }
    }

    companion object {
        const val STAGE_MS = 2000L
        const val ROW_GAP_MS = 400L
        const val FADE_MS = 300L
        const val LATE_MS = 3000L
        const val SLOW_OPEN_DELAY_MS = 1200L
        const val SLIDE_MS = 1500L
        const val JANK_MS = 700L
        const val SLOW_LIST_MS = 2500L
    }
}

/**
 * Pushed and popped with a custom 1500 ms slide. The window is translucent and the slide is
 * drawn by the screen itself (a TimeAnimator), so it takes 1.5 s even with animations off.
 */
class SlowDetailActivity : ScenarioActivity() {
    private var leaving = false
    private var slideIn: android.animation.TimeAnimator? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Slow detail", titleId = R.id.slow_detail_title)
        content.text(text = "This screen slid in over 1.5 seconds.", secondary = true)
        root.post {
            val w = root.width.toFloat()
            root.translationX = w
            slideIn = track(animateFor(SlowActivity.SLIDE_MS, { root.translationX = w * (1f - ease(it)) }))
        }
        interceptBack { slideOut() }
    }

    private fun slideOut() {
        if (leaving) return
        leaving = true
        slideIn?.cancel() // leaving mid-slide: slide back out from where it is
        val w = root.width.toFloat()
        val from = root.translationX / w.coerceAtLeast(1f)
        track(animateFor(SlowActivity.SLIDE_MS, { root.translationX = w * (from + (1f - from) * ease(it)) }) {
            finish()
            @Suppress("DEPRECATION")
            overridePendingTransition(0, 0)
        })
    }

    private fun ease(t: Float) = t * t * (3f - 2f * t)
}

/** A list that loads 2.5 s after opening, and 20 more rows 2.5 s after reaching its end. */
class SlowListActivity : ScenarioActivity() {
    private lateinit var rows: LinearLayout
    private var count = 0
    private var loading = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Slow list", titleId = R.id.screen_title)
        val center = content.spinner(R.id.slow_list_spinner, 48)
        val scroll = content.put(ScrollView(this).apply { id = R.id.slow_list }, h = 0, weight = 1f)
        rows = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        scroll.addView(rows)
        later(SlowActivity.SLOW_LIST_MS) {
            content.removeView(center)
            addRows()
        }
        scroll.setOnScrollChangeListener { v, _, _, _, _ ->
            if (!loading && count in 1 until MAX && !v.canScrollVertically(1)) {
                loading = true
                val footer = rows.spinner(R.id.slow_list_spinner, 40)
                later(SlowActivity.SLOW_LIST_MS) {
                    rows.removeView(footer)
                    addRows()
                    loading = false
                }
            }
        }
    }

    private fun addRows() {
        repeat(PAGE) {
            val n = ++count
            rows.put(TextView(this).apply {
                id = idOf("slow_item_$n")
                text = "Slow item $n"
                textSize = 18f
                setTextColor(color(R.color.text))
                gravity = android.view.Gravity.CENTER_VERTICAL
            }, h = dp(64), top = 0)
        }
    }

    companion object {
        const val PAGE = 20
        const val MAX = 40
    }
}
