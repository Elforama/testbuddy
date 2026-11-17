package dev.testbuddy.bench

import android.content.Context
import android.graphics.Canvas
import android.graphics.LinearGradient
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.Shader
import android.os.Bundle
import android.os.SystemClock
import android.view.View
import android.widget.FrameLayout
import android.widget.LinearLayout

/**
 * A feed's skeleton placeholders, three cards of gray blocks, with a light band sweeping across
 * them every [SWEEP_MS], forever. It is drawn on each frame from the clock, as a looping
 * animated image or a hand-made shimmer is: the device's animator duration scale (0 on test
 * devices) does not stop it, and it sends no accessibility event.
 */
class ShimmerView(context: Context) : View(context) {
    private val block = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = BASE }
    private val band = Paint(Paint.ANTI_ALIAS_FLAG)
    private val shift = Matrix()
    private var gradient: LinearGradient? = null

    init {
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        gradient = LinearGradient(0f, 0f, w / 3f, 0f, intArrayOf(BASE, HIGHLIGHT, BASE), null,
            Shader.TileMode.CLAMP)
        band.shader = gradient
    }

    override fun onDraw(canvas: Canvas) {
        val w = width.toFloat()
        val phase = (SystemClock.uptimeMillis() % SWEEP_MS).toFloat() / SWEEP_MS
        shift.setTranslate(-w / 3f + phase * (w + w / 3f), 0f)
        gradient?.setLocalMatrix(shift)
        val card = height / 3f
        val r = context.dp(8).toFloat()
        for (i in 0 until 3) {
            val top = i * card + context.dp(8)
            for (paint in listOf(block, band)) {
                canvas.drawRoundRect(0f, top, w, top + card * 0.55f, r, r, paint)
                canvas.drawRoundRect(0f, top + card * 0.62f, w * 0.8f, top + card * 0.7f, r, r, paint)
                canvas.drawRoundRect(0f, top + card * 0.76f, w * 0.55f, top + card * 0.84f, r, r, paint)
            }
        }
        postInvalidateOnAnimation()
    }

    private companion object {
        const val SWEEP_MS = 1300L
        const val BASE = 0xFFE0E0E0.toInt()
        const val HIGHLIGHT = 0xFFF7F7F7.toInt()
    }
}

/**
 * Android's Native screen, "Loading feed": a home feed whose posts never come in, with
 * [ShimmerView] (`feed_shimmer`) over most of the screen, between a still header line and a
 * still tab bar with a "Post" button (`feed_post`). `feed_details_open` "Show details" slides a
 * details panel (`feed_details`) in from the right over the whole screen, 300 ms after the tap,
 * for [SLIDE_MS] (issue #83).
 */
class LoadingFeedActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Loading feed")
        content.text(text = "Your feed", size = 18f, bold = true)
        val details = LinearLayout(this).apply {
            id = R.id.feed_details
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(0xFF263238.toInt())
            setPadding(dp(24), dp(96), dp(24), dp(24))
            visibility = View.INVISIBLE
        }
        details.text(text = "Details", size = 24f, bold = true).setTextColor(0xFFFFFFFF.toInt())
        details.text(text = "Posted by a neighbor").setTextColor(0xFFB0BEC5.toInt())
        content.button(R.id.feed_details_open, "Show details") {
            later(300) {
                details.translationX = root.width.toFloat()
                details.visibility = View.VISIBLE
                track(animateFor(SLIDE_MS, { f -> details.translationX = root.width * (1 - f) }))
            }
        }
        content.put(ShimmerView(this).apply {
            id = R.id.feed_shimmer
            contentDescription = "Loading posts"
        }, h = 0, weight = 1f)
        content.row {
            text(text = "Home", size = 16f, bold = true)
            text(text = "Search", size = 16f)
            text(text = "Profile", size = 16f)
            put(View(context), w = 0, h = 1, weight = 1f) // a spacer (a plain View's WRAP takes all)
            button(R.id.feed_post, "Post") {}
        }
        root.addView(details, FrameLayout.LayoutParams(MATCH, MATCH))
    }

    private companion object {
        const val SLIDE_MS = 2000L
    }
}
