package dev.testbuddy.bench

import android.animation.TimeAnimator
import android.annotation.SuppressLint
import android.app.Dialog
import android.content.Context
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.Editable
import android.text.TextWatcher
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.inputmethod.InputMethodManager
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.AppCompatEditText
import androidx.appcompat.widget.AppCompatTextView
import androidx.core.content.ContextCompat
import com.google.android.material.button.MaterialButton
import kotlin.math.roundToInt

// Small helpers for building the scenario screens in code. Every id is a resource id
// (res/values/ids.xml), so an `id:` selector matches it like an android:id from XML.

const val MATCH = ViewGroup.LayoutParams.MATCH_PARENT
const val WRAP = ViewGroup.LayoutParams.WRAP_CONTENT

fun Context.dp(v: Number): Int = (v.toFloat() * resources.displayMetrics.density).roundToInt()

/** A resource id by name, for the numbered ids (row_12, card_3_4, ...) declared in ids.xml. */
@SuppressLint("DiscouragedApi")
fun Context.idOf(name: String): Int {
    val id = resources.getIdentifier(name, "id", packageName)
    check(id != 0) { "No id resource named $name (add it to res/values/ids.xml)" }
    return id
}

fun Context.color(res: Int): Int = ContextCompat.getColor(this, res)

/** Adds [v] to this group with sensible params: full width in a vertical column, wrap in a row. */
fun <T : View> ViewGroup.put(v: T, w: Int? = null, h: Int = WRAP, top: Int = 8, weight: Float = 0f): T {
    val vertical = this is LinearLayout && orientation == LinearLayout.VERTICAL
    val width = w ?: if (vertical) MATCH else WRAP
    val lp: ViewGroup.LayoutParams = when (this) {
        is LinearLayout -> LinearLayout.LayoutParams(width, h, weight).apply {
            if (vertical) topMargin = context.dp(top) else marginEnd = context.dp(8)
        }
        is FrameLayout -> FrameLayout.LayoutParams(width, h)
        else -> ViewGroup.LayoutParams(width, h)
    }
    addView(v, lp)
    return v
}

fun ViewGroup.text(id: Int = View.NO_ID, text: CharSequence = "", size: Float = 16f, bold: Boolean = false,
                   secondary: Boolean = false): TextView =
    put(AppCompatTextView(context).apply {
        if (id != View.NO_ID) this.id = id
        this.text = text
        textSize = size
        setTextColor(context.color(if (secondary) R.color.text_secondary else R.color.text))
        if (bold) setTypeface(typeface, Typeface.BOLD)
    })

/** A section heading inside a scenario page (no id). */
fun ViewGroup.heading(text: String): TextView =
    text(text = text, size = 13f, bold = true, secondary = true).also {
        (it.layoutParams as? LinearLayout.LayoutParams)?.topMargin = context.dp(20)
    }

fun Context.materialButton(id: Int, text: String, flat: Boolean = false, onClick: ((View) -> Unit)? = null): MaterialButton {
    val b = if (flat) MaterialButton(this, null, androidx.appcompat.R.attr.borderlessButtonStyle)
    else MaterialButton(this)
    if (id != View.NO_ID) b.id = id
    b.text = text
    b.isAllCaps = false
    if (onClick != null) b.setOnClickListener(onClick)
    return b
}

fun ViewGroup.button(id: Int, text: String, flat: Boolean = false, w: Int? = WRAP, onClick: (View) -> Unit): MaterialButton =
    put(context.materialButton(id, text, flat, onClick), w = w)

fun ViewGroup.field(id: Int, hint: String, inputType: Int = android.text.InputType.TYPE_CLASS_TEXT,
                    onText: ((String) -> Unit)? = null): EditText =
    put(AppCompatEditText(context).apply {
        if (id != View.NO_ID) this.id = id
        this.hint = hint
        this.inputType = inputType
        importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
        if (onText != null) onTextChanged(onText)
    })

fun EditText.onTextChanged(block: (String) -> Unit) {
    addTextChangedListener(object : TextWatcher {
        override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
        override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
        override fun afterTextChanged(s: Editable?) = block(s?.toString() ?: "")
    })
}

fun ViewGroup.row(top: Int = 8, block: LinearLayout.() -> Unit): LinearLayout =
    put(LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        block()
    }, top = top)

fun ViewGroup.column(top: Int = 8, block: LinearLayout.() -> Unit): LinearLayout =
    put(LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        block()
    }, top = top)

/** The bench's indeterminate spinner (a RotateDrawable: it spins even with animations off). */
fun Context.spinner(id: Int = View.NO_ID): ProgressBar =
    ProgressBar(this, null, 0, R.style.Bench_Spinner).apply { if (id != View.NO_ID) this.id = id }

fun ViewGroup.spinner(id: Int = View.NO_ID, size: Int = 36): ProgressBar =
    put(context.spinner(id), w = context.dp(size), h = context.dp(size))

fun Context.rounded(color: Int, radius: Int = 12): GradientDrawable =
    GradientDrawable().apply { setColor(color); cornerRadius = dp(radius).toFloat() }

fun View.hideKeyboard() {
    context.getSystemService(InputMethodManager::class.java)?.hideSoftInputFromWindow(windowToken, 0)
}

/**
 * A fixed-length animation on a TimeAnimator: [frame] gets 0..1. Unlike ValueAnimator it takes
 * its real time even when the device's animator duration scale is 0 (test devices), so the
 * bench's "slow" and "late" timings hold everywhere.
 */
fun animateFor(durationMs: Long, frame: (Float) -> Unit, end: () -> Unit = {}): TimeAnimator =
    TimeAnimator().apply {
        setTimeListener { a, total, _ ->
            val f = (total.toFloat() / durationMs).coerceAtMost(1f)
            frame(f)
            if (f >= 1f) {
                a.cancel()
                end()
            }
        }
        start()
    }

/** Blocks the calling (main) thread for [ms] with a busy loop: deliberate jank. */
fun busyWait(ms: Long) {
    val until = System.nanoTime() + ms * 1_000_000L
    @Suppress("ControlFlowWithEmptyBody")
    while (System.nanoTime() < until) {
    }
}

/**
 * An in-app dialog whose content has the given root id (so `id: leave_dialog` matches it),
 * a message and buttons (id, text, action). Each button dismisses the dialog first.
 */
fun Context.inAppDialog(rootId: Int, message: String, vararg buttons: Triple<Int, String, () -> Unit>): Dialog {
    val dialog = Dialog(this)
    val root = LinearLayout(this).apply {
        id = rootId
        orientation = LinearLayout.VERTICAL
        setPadding(dp(24), dp(20), dp(24), dp(12))
        background = rounded(Color.WHITE, 24)
    }
    root.text(text = message, size = 20f, bold = true)
    root.row(top = 16) {
        gravity = Gravity.END
        for ((id, label, action) in buttons) {
            button(id, label, flat = true) { dialog.dismiss(); action() }
        }
    }
    dialog.setContentView(root)
    dialog.window?.setBackgroundDrawableResource(android.R.color.transparent)
    dialog.window?.setLayout(dp(320), WRAP)
    return dialog
}

/**
 * Base for every scenario screen: a header with Back (id `back`, top left) and the title
 * (`scenario_title` by default), then the body. With [scroll] the body is one vertical
 * ScrollView (id `page`). [root] is a full-screen FrameLayout for overlays.
 */
abstract class ScenarioActivity : AppCompatActivity() {
    protected val handler = Handler(Looper.getMainLooper())
    protected val animators = mutableListOf<TimeAnimator>()
    lateinit var root: FrameLayout
    lateinit var column: LinearLayout
    lateinit var titleView: TextView
    /** Where the scenario's content goes: the page's inner column, or [column] itself. */
    lateinit var content: LinearLayout

    /** Pad for the keyboard too (a typical app). The Keyboard scenario turns this off. */
    open val padForIme = true

    fun screen(title: String, titleId: Int = R.id.scenario_title, scroll: Boolean = false) {
        root = FrameLayout(this).apply { setBackgroundColor(color(R.color.background)) }
        column = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        root.addView(column, FrameLayout.LayoutParams(MATCH, MATCH))
        val header = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(4), dp(4), dp(16), dp(4))
        }
        header.addView(materialButton(R.id.back, getString(R.string.back), flat = true) { onBackButton() })
        titleView = AppCompatTextView(this).apply {
            id = titleId
            text = title
            textSize = 20f
            setTypeface(typeface, Typeface.BOLD)
            setTextColor(color(R.color.text))
        }
        header.addView(titleView, LinearLayout.LayoutParams(0, WRAP, 1f).apply { marginStart = dp(4) })
        column.addView(header, LinearLayout.LayoutParams(MATCH, WRAP))
        if (scroll) {
            val page = ScrollView(this).apply { id = R.id.page; isFillViewport = true }
            content = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(dp(16), 0, dp(16), dp(32))
            }
            page.addView(content, FrameLayout.LayoutParams(MATCH, WRAP))
            column.addView(page, LinearLayout.LayoutParams(MATCH, 0, 1f))
        } else {
            content = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(dp(16), 0, dp(16), dp(16))
            }
            column.addView(content, LinearLayout.LayoutParams(MATCH, 0, 1f))
        }
        setContentView(root)
        padColumn()
    }

    private fun padColumn() {
        column.setOnApplyWindowInsetsListener { v, insets ->
            if (Build.VERSION.SDK_INT >= 30) {
                val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
                val ime = if (padForIme) insets.getInsets(WindowInsets.Type.ime()).bottom else 0
                v.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime))
            } else {
                @Suppress("DEPRECATION")
                v.setPadding(insets.systemWindowInsetLeft, insets.systemWindowInsetTop,
                    insets.systemWindowInsetRight, insets.systemWindowInsetBottom)
            }
            insets
        }
        column.requestApplyInsets()
    }

    /** A status line pinned under the header, outside the scrolling page, so it stays on screen. */
    fun pinnedText(id: Int, text: String): TextView {
        val t = AppCompatTextView(this).apply {
            this.id = id
            this.text = text
            textSize = 18f
            setTypeface(typeface, Typeface.BOLD)
            setTextColor(color(R.color.text))
            setPadding(dp(16), 0, dp(16), dp(8))
        }
        column.addView(t, 1, LinearLayout.LayoutParams(MATCH, WRAP))
        return t
    }

    /** The header's Back button. Default: like the system back (so interceptors apply). */
    open fun onBackButton() = onBackPressedDispatcher.onBackPressed()

    fun later(ms: Long, block: () -> Unit) = handler.postDelayed(block, ms)

    fun track(a: TimeAnimator): TimeAnimator = a.also { animators += it }

    fun interceptBack(block: (OnBackPressedCallback) -> Unit): OnBackPressedCallback {
        val cb = object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() = block(this)
        }
        onBackPressedDispatcher.addCallback(this, cb)
        return cb
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.decorView.importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        animators.forEach { it.cancel() }
        super.onDestroy()
    }
}
