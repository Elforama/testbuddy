package dev.testbuddy.bench

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import android.view.Gravity
import android.view.View
import android.view.WindowManager
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatDialog
import com.google.android.material.bottomsheet.BottomSheetDialog
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.snackbar.Snackbar

/** Scenario 4, "Overlays": things drawn over the screen that take touches (or don't). */
class OverlaysActivity : ScenarioActivity() {
    private lateinit var result: TextView
    private lateinit var overlayStatus: TextView
    private var underTaps = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.OVERLAYS.title)
        result = content.text(R.id.overlay_result, "Nothing yet", size = 18f, bold = true)

        content.button(R.id.open_dialog, "Open dialog") { openDialog() }
        content.button(R.id.open_sheet, "Open sheet") { openSheet() }
        content.button(R.id.show_snackbar, "Show snackbar") { showSnackbar() }
        content.button(R.id.show_veil, "Show veil") { showVeil() }
        content.button(R.id.show_tooltip, "Show tooltip") { showTooltip(it) }
        content.button(R.id.open_cover, "Open cover") { openCover() }
        content.button(R.id.system_overlay, "Show floating bubble") { showBubble() }
        overlayStatus = content.text(R.id.perm_overlay_status, "", secondary = true)

        // The base screen's "Under" button: pinned to the bottom, under every overlay.
        content.put(View(this), h = 0, weight = 1f)
        val underCount = content.text(R.id.under_count, "Under taps: 0")
        content.button(R.id.under_button, "Under", w = MATCH) {
            underTaps++
            underCount.text = "Under taps: $underTaps"
        }
    }

    override fun onResume() {
        super.onResume()
        overlayStatus.text = if (Settings.canDrawOverlays(this)) "Overlay: allowed" else "Overlay: not allowed"
        Bubble.onClosed = { result.text = "Bubble closed" }
    }

    override fun onDestroy() {
        Bubble.onClosed = null
        super.onDestroy()
    }

    private fun openDialog() {
        val title = TextView(this).apply {
            id = R.id.dialog_title
            text = "Delete item?"
            textSize = 22f
            setTextColor(color(R.color.text))
            setPadding(dp(24), dp(24), dp(24), dp(8))
        }
        MaterialAlertDialogBuilder(this)
            .setCustomTitle(title)
            .setNegativeButton("Cancel") { _, _ -> result.text = "Cancelled" }
            .setPositiveButton("Delete") { _, _ -> result.text = "Deleted" }
            .show()
    }

    private fun openSheet() {
        val sheet = BottomSheetDialog(this)
        val body = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(24), dp(16), dp(24), dp(32))
        }
        body.text(R.id.sheet_title, "Sheet", size = 22f, bold = true)
        body.button(R.id.sheet_action, "Sheet action", w = MATCH) {
            result.text = "Sheet action"
            sheet.dismiss()
        }
        body.button(R.id.sheet_close, "Close", flat = true, w = MATCH) { sheet.dismiss() }
        sheet.setContentView(body)
        sheet.show()
    }

    private fun showSnackbar() {
        val bar = Snackbar.make(root, "Saved", SNACKBAR_MS)
            .setAction("Undo") { result.text = "Undone" }
        // Material's own ids are snackbar_text / snackbar_action; the spec names the action snackbar_undo.
        bar.view.findViewById<View>(com.google.android.material.R.id.snackbar_action)?.id = R.id.snackbar_undo
        bar.show()
    }

    /** A see-through dark veil with a spinner for 1.5 s that swallows every tap. */
    private fun showVeil() {
        val veil = FrameLayout(this).apply {
            id = R.id.veil
            setBackgroundColor(0x88000000.toInt())
            isClickable = true // swallow taps
            isFocusable = false
        }
        veil.addView(spinner(R.id.veil_spinner), FrameLayout.LayoutParams(dp(56), dp(56), Gravity.CENTER))
        root.addView(veil, FrameLayout.LayoutParams(MATCH, MATCH))
        later(VEIL_MS) { root.removeView(veil) }
    }

    /** A popover under the button; a tap anywhere only dismisses it (nothing passes through). */
    private fun showTooltip(anchor: View) {
        val catcher = FrameLayout(this).apply { isClickable = true }
        val tip = TextView(this).apply {
            id = R.id.tooltip_text
            text = "Tip: long press for more"
            textSize = 15f
            setTextColor(Color.WHITE)
            background = rounded(0xEE303033.toInt(), 8)
            setPadding(dp(12), dp(8), dp(12), dp(8))
        }
        val a = IntArray(2).also { anchor.getLocationInWindow(it) }
        val r = IntArray(2).also { root.getLocationInWindow(it) }
        catcher.addView(tip, FrameLayout.LayoutParams(WRAP, WRAP).apply {
            leftMargin = a[0] - r[0]
            topMargin = a[1] - r[1] + anchor.height + dp(4)
        })
        catcher.setOnClickListener { root.removeView(catcher) }
        root.addView(catcher, FrameLayout.LayoutParams(MATCH, MATCH))
    }

    /** A full-screen modal with a sheet that can open over it. */
    private fun openCover() {
        val cover = AppCompatDialog(this, R.style.Theme_Bench_Cover)
        val body = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(color(R.color.surface))
            setPadding(dp(24), dp(64), dp(24), dp(24))
        }
        body.text(R.id.cover_title, "Cover", size = 28f, bold = true)
        val coverResult = body.text(R.id.cover_result, "").apply { visibility = View.GONE }
        body.button(R.id.cover_sheet, "Open sheet on cover") {
            val sheet = BottomSheetDialog(this)
            val sb = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setPadding(dp(24), dp(16), dp(24), dp(32))
            }
            sb.text(R.id.cover_sheet_title, "Sheet on cover", size = 22f, bold = true)
            sb.button(R.id.cover_sheet_done, "Done", w = MATCH) {
                sheet.dismiss()
                coverResult.text = "Done on cover"
                coverResult.visibility = View.VISIBLE
            }
            sheet.setContentView(sb)
            sheet.show()
        }
        body.button(R.id.cover_close, "Close cover") { cover.dismiss() }
        cover.setContentView(body)
        cover.window?.setLayout(MATCH, MATCH)
        cover.show()
    }

    private fun showBubble() {
        if (Settings.canDrawOverlays(this)) {
            Bubble.show(applicationContext)
        } else {
            startActivity(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:$packageName")))
        }
    }

    companion object {
        const val SNACKBAR_MS = 3000
        const val VEIL_MS = 1500L
    }
}

/**
 * The floating bubble: a real TYPE_APPLICATION_OVERLAY window (SYSTEM_ALERT_WINDOW) at the
 * bottom right that stays over every app until it is tapped.
 */
object Bubble {
    private var view: View? = null
    var onClosed: (() -> Unit)? = null

    @SuppressLint("SetTextI18n")
    fun show(context: Context) {
        if (view != null) return
        val wm = context.getSystemService(WindowManager::class.java)
        val size = context.dp(160)
        val bubble = TextView(context).apply {
            id = R.id.bubble
            text = "Bubble"
            contentDescription = "Bubble"
            gravity = Gravity.CENTER
            textSize = 22f
            setTextColor(Color.WHITE)
            background = GradientDrawable().apply {
                shape = GradientDrawable.OVAL
                setColor(context.color(R.color.accent))
            }
        }
        bubble.setOnClickListener {
            wm.removeView(bubble)
            view = null
            onClosed?.invoke()
        }
        val lp = WindowManager.LayoutParams(
            size, size,
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE,
            PixelFormat.TRANSLUCENT,
        ).apply {
            gravity = Gravity.BOTTOM or Gravity.END
            x = context.dp(16)
            y = context.dp(96)
            title = "Bubble"
        }
        wm.addView(bubble, lp)
        view = bubble
    }
}
