package dev.testbuddy.bench

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.os.Build
import android.os.Bundle
import android.view.GestureDetector
import android.view.MotionEvent
import android.view.View
import android.widget.ImageButton
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.SeekBar
import android.widget.TextView
import androidx.appcompat.widget.AppCompatEditText
import androidx.appcompat.widget.AppCompatSeekBar

/**
 * A tap handler that leaves the view NOT clickable: no click action, no button role, nothing
 * for accessibility services to find. Deliberately bad (scenario 9).
 */
@SuppressLint("ClickableViewAccessibility")
fun View.onBareTap(block: (MotionEvent) -> Unit) {
    val detector = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {
        override fun onDown(e: MotionEvent) = true
        override fun onSingleTapUp(e: MotionEvent): Boolean { block(e); return true }
    })
    setOnTouchListener { _, e -> detector.onTouchEvent(e) }
    isClickable = false
    isLongClickable = false
}

/** "Secret offer" rendered into a bitmap: text only a screenshot can read. */
fun Context.offerBitmap(): Bitmap {
    val w = dp(220)
    val h = dp(64)
    val bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
    val c = Canvas(bmp)
    val paint = Paint(Paint.ANTI_ALIAS_FLAG)
    paint.color = color(R.color.accent)
    c.drawRoundRect(RectF(0f, 0f, w.toFloat(), h.toFloat()), dp(12).toFloat(), dp(12).toFloat(), paint)
    paint.color = android.graphics.Color.WHITE
    paint.textSize = dp(24).toFloat()
    paint.isFakeBoldText = true
    paint.textAlign = Paint.Align.CENTER
    c.drawText("Secret offer", w / 2f, h / 2f - (paint.descent() + paint.ascent()) / 2f, paint)
    return bmp
}

/** A 5-segment volume bar drawn on one canvas: no accessibility children, no value. */
class VolumeBar(context: Context, private val onLevel: (Int) -> Unit) : View(context) {
    private var level = 3
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG)

    init {
        onBareTap { e ->
            level = (e.x / width * SEGMENTS).toInt().coerceIn(0, SEGMENTS - 1) + 1
            invalidate()
            onLevel(level)
        }
    }

    override fun onDraw(canvas: Canvas) {
        val gap = context.dp(6).toFloat()
        val seg = (width - gap * (SEGMENTS - 1)) / SEGMENTS
        for (k in 0 until SEGMENTS) {
            paint.color = context.color(if (k < level) R.color.accent else R.color.divider)
            val left = k * (seg + gap)
            val top = height * (1f - (k + 1f) / SEGMENTS * 0.8f) - 1f
            canvas.drawRoundRect(RectF(left, top.coerceAtMost(height - 8f), left + seg, height.toFloat()), 6f, 6f, paint)
        }
    }

    companion object {
        const val SEGMENTS = 5
    }
}

/**
 * A card that is ONE accessibility element: it reports no children at all, so even a dump that
 * includes "not important" views (as UI Automator's does) only sees the card's merged label.
 */
class MergedCard(context: Context) : LinearLayout(context) {
    override fun addChildrenForAccessibility(outChildren: ArrayList<View>) {}
}

/** Scenario 9, "Poor accessibility": the Good form built badly on purpose. */
class PoorA11yActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.A11Y_POOR.title, scroll = true)
        val result = pinnedText(R.id.poor_result, "Nothing yet")
        fun say(s: String) { result.text = s }

        // Icon-only "buttons": images, no description, no id, not clickable.
        content.row(top = 12) {
            for ((icon, outcome) in listOf(R.drawable.ic_heart to "Liked", R.drawable.ic_share to "Shared",
                R.drawable.ic_trash to "Trashed")) {
                put(ImageView(context).apply {
                    setImageResource(icon)
                    setPadding(dp(10), dp(10), dp(10), dp(10))
                    onBareTap { say(outcome) }
                }, w = dp(48), h = dp(48))
            }
        }

        // A custom-drawn volume bar: one view, no children.
        content.put(VolumeBar(this) { say("Volume $it") }, w = dp(220), h = dp(40), top = 12)

        // A label that is not linked to its field; the field has no hint and no id.
        content.row(top = 12) {
            text(text = "Nickname")
            put(AppCompatEditText(context).apply {
                importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
                onTextChanged { say("Nickname: $it") }
            }, w = 0, weight = 1f)
        }

        // Tappable plain text.
        content.text(text = "Continue", size = 18f).apply {
            setTextColor(color(R.color.accent))
            setPadding(0, dp(8), 0, dp(8))
            onBareTap { say("Continued") }
        }

        // Three identical "Edit" buttons, told apart only by the title above each.
        for (name in listOf("Alpha", "Beta", "Gamma")) {
            content.column(top = 4) {
                text(text = name, bold = true)
                button(View.NO_ID, "Edit") { say("Edit $name") }.apply {
                    (layoutParams as LinearLayout.LayoutParams).topMargin = 0
                }
            }
        }

        // A card that is ONE accessibility element: its children are hidden from the tree.
        val card = content.put(MergedCard(this).apply {
            orientation = LinearLayout.VERTICAL
            background = rounded(color(R.color.surface), 12)
            setPadding(dp(16), dp(12), dp(16), dp(12))
            isFocusable = true
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
            contentDescription = "Order 42, 3 items, Track"
        }, top = 12)
        card.column(top = 0) {
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
            text(text = "Order 42", size = 18f, bold = true)
            row(top = 4) {
                put(TextView(context).apply {
                    text = "3 items"
                    textSize = 16f
                    setTextColor(color(R.color.text_secondary))
                }, w = 0, weight = 1f)
                button(View.NO_ID, "Track") { say("Tracking 42") }
            }
        }

        // A 12 x 12 dp target.
        content.row(top = 12) {
            text(text = "Tiny:")
            put(View(context).apply {
                background = rounded(color(R.color.accent), 6)
                onBareTap { say("Tiny tapped") }
            }, w = dp(12), h = dp(12))
        }

        // Words in an image, no description.
        content.put(ImageView(this).apply {
            setImageBitmap(offerBitmap())
            scaleType = ImageView.ScaleType.FIT_START
            onBareTap { say("Offer opened") }
        }, w = dp(220), h = dp(64), top = 12)
    }
}

/** Scenario 10, "Good accessibility": the same controls with ids, labels, roles and state. */
class GoodA11yActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.A11Y_GOOD.title, scroll = true)
        val result = pinnedText(R.id.good_result, "Nothing yet").apply {
            accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
        }
        fun say(s: String) { result.text = s }

        content.row(top = 12) {
            for ((id, icon, label, outcome) in listOf(
                Quad(R.id.good_like, R.drawable.ic_heart, "Like", "Liked"),
                Quad(R.id.good_share, R.drawable.ic_share, "Share", "Shared"),
                Quad(R.id.good_trash, R.drawable.ic_trash, "Trash", "Trashed"),
            )) {
                put(ImageButton(context).apply {
                    this.id = id
                    setImageResource(icon)
                    contentDescription = label
                    setBackgroundResource(android.R.drawable.list_selector_background)
                    setOnClickListener { say(outcome) }
                }, w = dp(48), h = dp(48))
            }
        }

        content.text(text = "Volume", bold = true).apply { labelFor = R.id.good_volume }
        content.put(AppCompatSeekBar(this).apply {
            id = R.id.good_volume
            contentDescription = "Volume"
            max = 4
            progress = 2
            stateText(this, 3)
            setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
                override fun onProgressChanged(s: SeekBar, p: Int, fromUser: Boolean) {
                    stateText(s, p + 1)
                    say("Volume ${p + 1}")
                }
                override fun onStartTrackingTouch(s: SeekBar) {}
                override fun onStopTrackingTouch(s: SeekBar) {}
            })
        }, w = dp(240), h = dp(48))

        content.text(text = "Nickname", bold = true).apply { labelFor = R.id.good_nickname }
        content.field(R.id.good_nickname, "Nickname") { say("Nickname: $it") }

        content.button(R.id.good_continue, "Continue") { say("Continued") }

        for ((id, name) in listOf(R.id.edit_alpha to "Alpha", R.id.edit_beta to "Beta", R.id.edit_gamma to "Gamma")) {
            content.row(top = 4) {
                put(TextView(context).apply {
                    text = name
                    textSize = 16f
                    setTextColor(color(R.color.text))
                }, w = 0, weight = 1f)
                button(id, "Edit") { say("Edit $name") }.contentDescription = "Edit $name"
            }
        }

        content.column(top = 12) {
            id = R.id.order_card
            background = rounded(color(R.color.surface), 12)
            setPadding(dp(16), dp(12), dp(16), dp(12))
            text(text = "Order 42", size = 18f, bold = true)
            row(top = 4) {
                put(TextView(context).apply {
                    text = "3 items"
                    textSize = 16f
                    setTextColor(color(R.color.text_secondary))
                }, w = 0, weight = 1f)
                button(R.id.order_track, "Track") { say("Tracking 42") }.contentDescription = "Track order 42"
            }
        }

        content.row(top = 12) {
            text(text = "Tiny:")
            put(context.materialButton(R.id.good_tiny, "Tiny", flat = true) { say("Tiny tapped") }.apply {
                minWidth = dp(48)
                minHeight = dp(48)
            })
        }

        content.put(ImageView(this).apply {
            id = R.id.good_offer
            setImageBitmap(offerBitmap())
            contentDescription = "Secret offer"
            isClickable = true
            isFocusable = true
            setOnClickListener { say("Offer opened") }
        }, w = dp(220), h = dp(64), top = 12)

        content.heading("Live region")
        val status = content.text(R.id.good_status, "Not saved").apply {
            accessibilityLiveRegion = View.ACCESSIBILITY_LIVE_REGION_POLITE
        }
        content.button(R.id.good_save, "Save") {
            status.text = "Saving…"
            later(SAVE_MS) { status.text = "Saved" }
        }
    }

    private fun stateText(v: View, level: Int) {
        if (Build.VERSION.SDK_INT >= 30) v.stateDescription = "Volume $level"
    }

    private data class Quad(val id: Int, val icon: Int, val label: String, val outcome: String)

    companion object {
        const val SAVE_MS = 1000L
    }
}
