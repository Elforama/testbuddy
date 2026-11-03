package dev.testbuddy.bench

import android.os.Bundle
import android.text.InputType
import android.view.View
import android.widget.ImageView
import android.widget.TextView
import android.widget.Toast

/** Scenario 13, "Errors and retries". */
class ErrorsActivity : ScenarioActivity() {
    private lateinit var message: TextView
    private lateinit var spinner: View
    private lateinit var retry: View
    private lateinit var illustration: View
    private var loads = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.ERRORS.title, scroll = true)
        // The result line stays on screen under the header while the page scrolls.
        message = pinnedText(R.id.err_message, "").apply { visibility = View.GONE }
        spinner = spinner(R.id.err_spinner).apply { visibility = View.GONE }
        column.addView(spinner, 2, android.widget.LinearLayout.LayoutParams(dp(36), dp(36)).apply {
            marginStart = dp(16)
            bottomMargin = dp(8)
        })
        illustration = content.put(ImageView(this).apply {
            id = R.id.err_illustration
            setImageResource(R.drawable.logo)
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO // decorative
            visibility = View.GONE
        }, w = dp(96), h = dp(96))

        content.heading("Flaky load")
        content.button(R.id.err_load, "Load data") { load() }
        retry = content.button(R.id.err_retry, "Retry") { load() }.apply { visibility = View.GONE }

        content.heading("Form errors")
        val email = content.field(R.id.err_email, "Email", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_EMAIL_ADDRESS)
        val emailError = content.text(R.id.err_email_error, "Invalid email", size = 14f).apply {
            setTextColor(color(R.color.error)); visibility = View.GONE
        }
        val age = content.field(R.id.err_age, "Age", InputType.TYPE_CLASS_NUMBER)
        val ageError = content.text(R.id.err_age_error, "Must be 18 or older", size = 14f).apply {
            setTextColor(color(R.color.error)); visibility = View.GONE
        }
        val formStatus = content.text(R.id.err_form_status, "").apply { visibility = View.GONE }
        content.button(R.id.err_form_submit, "Submit") {
            it.hideKeyboard()
            val badEmail = !email.text.contains('@')
            val badAge = (age.text.toString().toIntOrNull() ?: 0) < 18
            emailError.visibility = if (badEmail) View.VISIBLE else View.GONE
            ageError.visibility = if (badAge) View.VISIBLE else View.GONE
            formStatus.text = if (badEmail || badAge) "" else "Form sent"
            formStatus.visibility = if (badEmail || badAge) View.GONE else View.VISIBLE
        }

        content.heading("Empty state")
        content.button(R.id.err_empty, "Show empty") {
            show("No results")
            illustration.visibility = View.VISIBLE
        }

        content.heading("Timeout")
        content.button(R.id.err_timeout, "Slow request") {
            busy()
            later(TIMEOUT_MS) { show("Request timed out") }
        }

        content.heading("Toast")
        content.button(R.id.err_toast, "Fail quietly") {
            // A real Toast: drawn by the system, not part of this app's accessibility tree.
            Toast.makeText(this, "Something went wrong", Toast.LENGTH_SHORT).show()
        }

        content.heading("Freeze")
        content.button(R.id.err_freeze, "Freeze 6 s") {
            busyWait(FREEZE_MS)
            show("Thawed")
        }
    }

    /** Fails the first time on this screen, then succeeds. */
    private fun load() {
        retry.visibility = View.GONE
        busy()
        later(LOAD_MS) {
            loads++
            if (loads == 1) {
                show("Couldn't load. Try again.")
                retry.visibility = View.VISIBLE
            } else {
                show("Loaded 3 items")
            }
        }
    }

    private fun busy() {
        message.visibility = View.GONE
        illustration.visibility = View.GONE
        spinner.visibility = View.VISIBLE
    }

    private fun show(text: String) {
        spinner.visibility = View.GONE
        illustration.visibility = View.GONE
        message.text = text
        message.visibility = View.VISIBLE
    }

    companion object {
        const val LOAD_MS = 800L
        const val TIMEOUT_MS = 6000L
        const val FREEZE_MS = 6000L
    }
}
