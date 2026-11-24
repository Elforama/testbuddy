package dev.testbuddy.bench

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.os.Bundle
import android.view.View
import android.view.accessibility.AccessibilityNodeInfo

class SilentLabel(context: Context) : View(context) {
    var label: String = ""
        set(value) {
            field = value
            invalidate()
        }

    private val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        textSize = 18f * resources.displayMetrics.scaledDensity
        color = context.color(R.color.text)
    }

    init {
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_YES
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        setMeasuredDimension(MeasureSpec.getSize(widthMeasureSpec), context.dp(40))
    }

    override fun onDraw(canvas: Canvas) {
        canvas.drawText(label, 0f, height / 2f - (paint.ascent() + paint.descent()) / 2f, paint)
    }

    override fun onInitializeAccessibilityNodeInfo(info: AccessibilityNodeInfo) {
        super.onInitializeAccessibilityNodeInfo(info)
        info.text = label
    }
}

class SilentTextActivity : ScenarioActivity() {
    private var count = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Silent text")
        val label = content.put(SilentLabel(this).apply {
            id = R.id.tk_silent_label
            label = "Silent count 0"
        }, w = MATCH)
        content.button(R.id.tk_silent_bump, "Change silently") {
            later(CHANGE_MS) {
                count++
                label.label = "Silent count $count"
            }
        }
    }

    private companion object {
        const val CHANGE_MS = 500L
    }
}
