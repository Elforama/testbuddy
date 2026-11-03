package dev.testbuddy.bench

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ProgressBar
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import androidx.viewpager2.widget.ViewPager2
import kotlin.math.PI
import kotlin.math.cos
import kotlin.math.exp
import kotlin.math.sin
import kotlin.math.sqrt

/**
 * Scenario 12, "Animations". Every animation runs on a TimeAnimator or Handler, so it keeps
 * moving (and keeps its timing) even with the device's animations turned off.
 */
class AnimationsActivity : ScenarioActivity() {
    private lateinit var status: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.ANIMATIONS.title, scroll = true)
        status = pinnedText(R.id.anim_status, "Idle")

        content.heading("Spinner")
        content.spinner(R.id.anim_spinner, 40)

        content.heading("Marquee")
        marquee()

        content.heading("Pulse")
        val pulse = content.button(R.id.anim_pulse, "Pulse") { status.text = "Pulse tapped" }
        track(forever { ms ->
            val p = (ms % PULSE_MS).toFloat() / PULSE_MS
            val s = 1f + 0.15f * (0.5f - 0.5f * cos(2 * PI * p).toFloat())
            pulse.scaleX = s
            pulse.scaleY = s
        })

        content.heading("Spring in")
        springIn()

        content.heading("Fade in")
        lateinit var faded: View
        content.button(R.id.anim_fade_open, "Fade in") {
            track(animateFor(FADE_MS, { faded.alpha = it }))
        }
        faded = content.button(R.id.anim_faded, "Faded") { status.text = "Faded tapped" }.apply { alpha = 0f }

        content.heading("Layout shift")
        layoutShift()

        content.heading("Auto carousel")
        carousel()

        content.heading("Progress")
        val bar = content.put(ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            id = R.id.anim_progress
            max = 1000
        })
        track(forever { ms -> bar.progress = ((ms % PROGRESS_MS) * 1000 / PROGRESS_MS).toInt() })

        content.heading("Confetti")
        content.button(R.id.anim_confetti, "Celebrate") { confetti() }
        content.button(R.id.anim_under, "Under confetti") { status.text = "Under tapped" }
    }

    private fun marquee() {
        val clip = content.put(FrameLayout(this).apply { clipChildren = true }, h = dp(32))
        val text = TextView(this).apply {
            id = R.id.anim_marquee
            text = "Breaking news: everything is fine"
            textSize = 18f
            setTextColor(color(R.color.text))
            maxLines = 1
        }
        clip.addView(text, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.CENTER_VERTICAL))
        track(forever { ms ->
            val span = clip.width + text.width
            if (span > 0) text.translationX = clip.width - (ms * dp(60) / 1000f) % span
        })
    }

    /** "Sprung" comes in from off-screen right on an under-damped spring: overshoots, settles in ~900 ms. */
    private fun springIn() {
        lateinit var lane: FrameLayout
        lateinit var sprung: View
        content.button(R.id.anim_spring_open, "Spring in") {
            sprung.visibility = View.VISIBLE
            val from = lane.width.toFloat()
            track(animateFor(SPRING_MS, { f ->
                val t = f * SPRING_MS / 1000f
                val zeta = 0.35f
                val omega = 12f
                val wd = omega * sqrt(1 - zeta * zeta)
                val x = exp(-zeta * omega * t) * (cos(wd * t) + zeta * omega / wd * sin(wd * t))
                sprung.translationX = if (f >= 1f) 0f else from * x
            }))
        }
        lane = content.put(FrameLayout(this), h = dp(56))
        sprung = materialButton(R.id.anim_sprung, "Sprung") { status.text = "Sprung tapped" }.apply {
            visibility = View.GONE
        }
        lane.addView(sprung, FrameLayout.LayoutParams(WRAP, WRAP, Gravity.CENTER_VERTICAL))
    }

    /** 600 ms after "Shift", a 200 dp banner grows above "Target" over 400 ms, pushing it down. */
    private fun layoutShift() {
        lateinit var banner: TextView
        content.button(R.id.anim_shift_open, "Shift") {
            later(SHIFT_DELAY_MS) {
                banner.visibility = View.VISIBLE
                track(animateFor(SHIFT_MS, { f ->
                    banner.layoutParams = banner.layoutParams.apply { height = (dp(200) * f).toInt() }
                }))
            }
        }
        banner = content.put(TextView(this).apply {
            id = R.id.anim_banner
            text = "Banner"
            gravity = Gravity.CENTER
            textSize = 20f
            setTextColor(color(R.color.text))
            background = rounded(color(R.color.surface), 12)
            visibility = View.GONE
        }, h = 0)
        content.button(R.id.anim_target, "Target") { status.text = "Target tapped" }
    }

    private fun carousel() {
        var running = true
        val pager = content.put(ViewPager2(this).apply {
            id = R.id.anim_carousel
            adapter = object : RecyclerView.Adapter<RecyclerView.ViewHolder>() {
                override fun getItemCount() = SLIDES
                override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) =
                    object : RecyclerView.ViewHolder(TextView(parent.context).apply {
                        layoutParams = ViewGroup.LayoutParams(MATCH, MATCH)
                        gravity = Gravity.CENTER
                        textSize = 22f
                        setTextColor(color(R.color.text))
                        background = rounded(color(R.color.surface), 16)
                    }) {}
                override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) {
                    (holder.itemView as TextView).apply {
                        id = idOf("slide_${position + 1}")
                        text = "Slide ${position + 1}"
                    }
                }
            }
        }, h = dp(140))
        val advance = object : Runnable {
            override fun run() {
                if (!running) return
                pager.currentItem = (pager.currentItem + 1) % SLIDES
                handler.postDelayed(this, CAROUSEL_MS)
            }
        }
        handler.postDelayed(advance, CAROUSEL_MS)
        content.button(R.id.anim_carousel_tap, "Stop") {
            running = false
            handler.removeCallbacks(advance)
            status.text = "Stopped at Slide ${pager.currentItem + 1}"
        }
    }

    private fun confetti() {
        val view = Confetti(this)
        root.addView(view, FrameLayout.LayoutParams(MATCH, MATCH))
        track(animateFor(CONFETTI_MS, { view.progress = it; view.invalidate() }) { root.removeView(view) })
    }

    /** 60 falling particles over the whole screen. Not touchable, not in the accessibility tree. */
    private class Confetti(context: Context) : View(context) {
        var progress = 0f
        private val paint = Paint(Paint.ANTI_ALIAS_FLAG)
        private val colors = intArrayOf(0xFFE53935.toInt(), 0xFF3D5AFE.toInt(), 0xFFFFB300.toInt(), 0xFF43A047.toInt())

        init {
            id = R.id.confetti
            importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_NO
            isClickable = false
            isFocusable = false
        }

        override fun onDraw(canvas: Canvas) {
            val size = context.dp(8).toFloat()
            for (i in 0 until 60) {
                // Deterministic: no randomness, each particle's lane and delay from its index.
                val x0 = (i * 37 % 100) / 100f * width
                val delay = (i * 13 % 40) / 100f
                val t = ((progress - delay) / (1f - delay)).coerceIn(0f, 1f)
                val y = -size + t * (height + 2 * size)
                val x = x0 + sin((t * 6 + i).toDouble()).toFloat() * context.dp(16)
                paint.color = colors[i % colors.size]
                canvas.drawRect(x, y, x + size, y + size * 1.6f, paint)
            }
        }

        override fun onTouchEvent(event: android.view.MotionEvent?) = false
    }

    companion object {
        const val PULSE_MS = 800L
        const val SPRING_MS = 900L
        const val FADE_MS = 1500L
        const val SHIFT_DELAY_MS = 600L
        const val SHIFT_MS = 400L
        const val SLIDES = 5
        const val CAROUSEL_MS = 1500L
        const val PROGRESS_MS = 4000L
        const val CONFETTI_MS = 2500L
    }
}
