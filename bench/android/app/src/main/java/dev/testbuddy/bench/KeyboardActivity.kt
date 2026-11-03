package dev.testbuddy.bench

import android.os.Bundle
import android.text.InputType
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.EditText
import android.widget.TextView

/**
 * Scenario 14, "Keyboard". The screen does not resize or pad for the keyboard
 * (adjustNothing), so the soft keyboard covers the bottom field and its Submit button.
 */
class KeyboardActivity : ScenarioActivity() {
    override val padForIme = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.KEYBOARD.title)
        val status = content.text(R.id.kb_status, "Nothing typed", size = 18f, bold = true)

        // A Next chain: Next moves on, Done on the last hides the keyboard.
        content.heading("Next chain")
        val first = content.field(R.id.kb_first, "First")
        val second = content.field(R.id.kb_second, "Second")
        val third = content.field(R.id.kb_third, "Third")
        for ((field, next) in listOf(first to second, second to third)) {
            field.isSingleLine = true
            field.imeOptions = EditorInfo.IME_ACTION_NEXT
            field.onEditorAction(EditorInfo.IME_ACTION_NEXT) { next.requestFocus() }
        }
        third.isSingleLine = true
        third.imeOptions = EditorInfo.IME_ACTION_DONE
        third.onEditorAction(EditorInfo.IME_ACTION_DONE) {
            third.hideKeyboard()
            status.text = "Chain done: ${first.text}/${second.text}/${third.text}"
        }

        content.heading("Multiline")
        val lines = TextView(this)
        content.field(R.id.kb_multiline, "Long text",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE) {
            lines.text = "Lines: ${if (it.isEmpty()) 0 else it.split('\n').size}"
        }.apply { maxLines = 4 }
        content.put(lines.apply {
            id = R.id.kb_lines
            text = "Lines: 0"
            textSize = 16f
            setTextColor(color(R.color.text))
        })

        content.heading("Autofocus")
        content.button(R.id.kb_autofocus_open, "Open autofocus") {
            startActivity(android.content.Intent(this, AutofocusActivity::class.java))
        }

        // Pinned near the bottom: the keyboard covers these.
        content.put(View(this), h = 0, weight = 1f)
        content.field(R.id.kb_bottom, "Bottom field") { status.text = "Bottom: $it" }.isSingleLine = true
        content.button(R.id.kb_submit, "Submit", w = MATCH) { status.text = "Submitted" }
    }

    /**
     * Runs [block] for the IME action [action], or for Enter from a hardware key / key event
     * (which arrives as IME_NULL). Consumes both so focus does not also move by itself.
     */
    private fun EditText.onEditorAction(action: Int, block: () -> Unit) {
        // The keyboard may turn an Enter key's DOWN into the editor action (focus moves on) and
        // let its UP reach the next field, which would advance focus again: ignore an Enter UP
        // whose DOWN this field never saw.
        var enterDown = false
        setOnKeyListener { _, code, event ->
            if (code != KeyEvent.KEYCODE_ENTER) return@setOnKeyListener false
            if (event.action == KeyEvent.ACTION_DOWN) {
                enterDown = true
                false
            } else {
                val stray = !enterDown
                enterDown = false
                stray
            }
        }
        setOnEditorActionListener { _, id, event ->
            when {
                id == action -> { block(); true }
                id == EditorInfo.IME_NULL && event?.keyCode == KeyEvent.KEYCODE_ENTER -> {
                    // Act on the key's UP: acting on DOWN would move focus and let the UP
                    // advance focus once more from the next field.
                    if (event.action == KeyEvent.ACTION_UP) block()
                    true
                }
                else -> false
            }
        }
    }
}

/** Opens with its field focused and the keyboard up. */
class AutofocusActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Autofocus", titleId = R.id.screen_title)
        val field = content.field(R.id.kb_auto, "Focused at start")
        field.requestFocus()
        field.post {
            getSystemService(InputMethodManager::class.java)?.showSoftInput(field, InputMethodManager.SHOW_IMPLICIT)
        }
    }
}
