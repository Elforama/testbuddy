package dev.testbuddy.bench

import android.animation.TimeAnimator
import android.app.Activity
import android.content.Intent
import android.graphics.Typeface
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.InputMethodManager
import android.widget.EditText

class LoginActivity : Activity() {
    private val handler = Handler(Looper.getMainLooper())
    private var logoSpin: TimeAnimator? = null
    private lateinit var logo: View

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_login)
        // No system UI over the app (spec): keep autofill and its "Save password?" sheet out.
        window.decorView.importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS
        (findViewById<ViewGroup>(android.R.id.content).getChildAt(0) as ViewGroup)
            .getChildAt(0).padForSystemBars()

        logo = findViewById(R.id.logo_spinner)
        val username = findViewById<EditText>(R.id.username)
        val password = findViewById<EditText>(R.id.password)
        password.typeface = Typeface.DEFAULT // textPassword makes the hint monospace
        val error = findViewById<View>(R.id.error)
        val loading = findViewById<View>(R.id.loading)

        findViewById<View>(R.id.login).setOnClickListener {
            hideKeyboard()
            val name = username.text.toString()
            if (name.isEmpty() || password.text.isEmpty()) {
                error.visibility = View.VISIBLE
                return@setOnClickListener
            }
            error.visibility = View.GONE
            loading.visibility = View.VISIBLE
            handler.postDelayed({
                loading.visibility = View.GONE
                getSystemService(android.view.autofill.AutofillManager::class.java)?.cancel()
                startActivity(Intent(this, HomeActivity::class.java).putExtra(EXTRA_USERNAME, name))
                finish()
            }, SIGN_IN_DELAY_MS)
        }
    }

    override fun onStart() {
        super.onStart()
        // One turn every 1.5 s, forever.
        logoSpin = forever { ms -> logo.rotation = (ms % 1500L) * 360f / 1500f }
    }

    override fun onStop() {
        logoSpin?.cancel()
        logoSpin = null
        super.onStop()
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    private fun hideKeyboard() {
        val focus = currentFocus ?: return
        getSystemService(InputMethodManager::class.java)?.hideSoftInputFromWindow(focus.windowToken, 0)
    }

    companion object {
        const val SIGN_IN_DELAY_MS = 1000L
    }
}
