package dev.testbuddy.bench

import android.animation.TimeAnimator
import android.app.Activity
import android.os.Bundle
import android.view.Choreographer
import android.view.View
import android.view.ViewGroup
import android.widget.Switch
import android.widget.TextView
import java.util.Locale

/** The stress screen: the stopwatch changes every frame and the ball never stops. */
class LiveActivity : Activity() {
    private lateinit var stopwatch: TextView
    private lateinit var ball: View
    private var ballMove: TimeAnimator? = null
    private var openedNanos = 0L
    private var taps = 0

    private val frame = object : Choreographer.FrameCallback {
        override fun doFrame(frameTimeNanos: Long) {
            val ms = ((frameTimeNanos - openedNanos) / 1_000_000L).coerceAtLeast(0L)
            stopwatch.text = String.format(
                Locale.US, "%02d:%02d.%03d", ms / 60_000L, ms / 1000L % 60L, ms % 1000L,
            )
            Choreographer.getInstance().postFrameCallback(this)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        openedNanos = System.nanoTime()
        setContentView(R.layout.activity_live)
        findViewById<ViewGroup>(android.R.id.content).getChildAt(0).padForSystemBars()

        stopwatch = findViewById(R.id.stopwatch)
        ball = findViewById(R.id.ball)

        val tapCount = findViewById<TextView>(R.id.tap_count)
        tapCount.text = getString(R.string.tapped, taps)
        findViewById<View>(R.id.tap_me).setOnClickListener {
            taps++
            tapCount.text = getString(R.string.tapped, taps)
        }

        val label = findViewById<TextView>(R.id.notify_label)
        @Suppress("DEPRECATION")
        findViewById<Switch>(R.id.notify_switch).setOnCheckedChangeListener { _, on ->
            label.setText(if (on) R.string.notifications_on else R.string.notifications_off)
        }

        findViewById<View>(R.id.back).setOnClickListener { finish() }
    }

    override fun onStart() {
        super.onStart()
        Choreographer.getInstance().postFrameCallback(frame)
        // Back and forth across the track: 1.2 s each way, forever.
        ballMove = forever { ms ->
            val track = ball.parent as View
            val travel = (track.width - track.paddingLeft - track.paddingRight - ball.width).coerceAtLeast(0)
            val phase = (ms % (2 * BALL_LEG_MS)).toFloat() / BALL_LEG_MS // 0..2
            val p = if (phase <= 1f) phase else 2f - phase
            val eased = p * p * (3f - 2f * p) // smoothstep
            ball.translationX = eased * travel
        }
    }

    override fun onStop() {
        Choreographer.getInstance().removeFrameCallback(frame)
        ballMove?.cancel()
        ballMove = null
        super.onStop()
    }

    companion object {
        const val BALL_LEG_MS = 1200L
    }
}
