package dev.testbuddy.bench

import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.widget.AppCompatCheckBox
import androidx.appcompat.widget.AppCompatEditText
import androidx.recyclerview.widget.RecyclerView
import androidx.viewpager2.widget.ViewPager2
import com.google.android.material.button.MaterialButton

/** Scenario 7, "Onboarding and login": a pager (once per process), sign in, a 6-box OTP. */
class OnboardingActivity : ScenarioActivity() {
    private lateinit var onboarding: LinearLayout
    private lateinit var signIn: LinearLayout
    private lateinit var otp: LinearLayout
    private lateinit var done: LinearLayout

    private lateinit var email: EditText
    private lateinit var password: EditText
    private lateinit var remember: AppCompatCheckBox
    private lateinit var submit: MaterialButton
    private lateinit var error: TextView
    private lateinit var spinner: View
    private lateinit var boxes: List<EditText>
    private lateinit var otpError: TextView
    private lateinit var signedIn: TextView
    private var updatingBoxes = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.ONBOARDING.title, scroll = true)
        buildOnboarding()
        buildSignIn()
        buildOtp()
        buildDone()
        show(if (AppState.onboardingSeen) signIn else onboarding)
    }

    private fun show(state: View) {
        for (s in listOf(onboarding, signIn, otp, done)) s.visibility = if (s === state) View.VISIBLE else View.GONE
    }

    private fun buildOnboarding() {
        val titles = listOf("Welcome", "Track things", "Stay in sync")
        val blurbs = listOf("Buddy Bench in three pages.", "Lists, carts and counters.", "Everything stays on this device.")
        onboarding = content.column { }
        val indicator = TextView(this)
        lateinit var next: MaterialButton
        val pager = ViewPager2(this).apply {
            id = R.id.pager
            adapter = object : RecyclerView.Adapter<RecyclerView.ViewHolder>() {
                override fun getItemCount() = 3
                override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): RecyclerView.ViewHolder {
                    val page = LinearLayout(parent.context).apply {
                        orientation = LinearLayout.VERTICAL
                        gravity = Gravity.CENTER
                        layoutParams = ViewGroup.LayoutParams(MATCH, MATCH)
                        background = rounded(color(R.color.surface), 16)
                    }
                    page.text(size = 28f, bold = true).gravity = Gravity.CENTER
                    page.text(secondary = true).gravity = Gravity.CENTER
                    return object : RecyclerView.ViewHolder(page) {}
                }
                override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) {
                    val page = holder.itemView as LinearLayout
                    (page.getChildAt(0) as TextView).apply { id = idOf("page_${position + 1}"); text = titles[position] }
                    (page.getChildAt(1) as TextView).text = blurbs[position]
                }
            }
            registerOnPageChangeCallback(object : ViewPager2.OnPageChangeCallback() {
                override fun onPageSelected(position: Int) {
                    indicator.text = "Page ${position + 1} of 3"
                    val last = position == 2
                    next.id = if (last) R.id.onboarding_done else R.id.onboarding_next
                    next.text = if (last) "Get started" else "Next"
                }
            })
        }
        onboarding.put(PageTurner(this, pager), h = dp(320))
        onboarding.put(indicator.apply {
            id = R.id.pager_indicator
            text = "Page 1 of 3"
            textSize = 16f
            gravity = Gravity.CENTER
            setTextColor(color(R.color.text))
        })
        onboarding.row(top = 16) {
            button(R.id.onboarding_skip, "Skip", flat = true) { finishOnboarding() }
            put(View(context), w = 0, h = 1, weight = 1f)
            next = button(R.id.onboarding_next, "Next") {
                if (pager.currentItem < 2) pager.currentItem += 1 else finishOnboarding()
            }
        }
    }

    private fun finishOnboarding() {
        AppState.onboardingSeen = true
        show(signIn)
    }

    private fun buildSignIn() {
        signIn = content.column { }
        signIn.text(text = "Sign in", size = 24f, bold = true)
        email = signIn.field(R.id.signin_email, "Email",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_EMAIL_ADDRESS) { updateSubmit() }
        password = signIn.field(R.id.signin_password, "Password",
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD) { updateSubmit() }
        password.typeface = android.graphics.Typeface.DEFAULT
        remember = signIn.put(AppCompatCheckBox(this).apply { id = R.id.signin_remember; text = "Remember me" })
        submit = signIn.button(R.id.signin_submit, "Sign in", w = MATCH) { submit() }
        submit.isEnabled = false
        spinner = signIn.spinner(R.id.signin_spinner).apply { visibility = View.GONE }
        error = signIn.text(R.id.signin_error, "").apply {
            setTextColor(color(R.color.error))
            visibility = View.GONE
        }
    }

    private fun updateSubmit() {
        submit.isEnabled = email.text.isNotEmpty() && password.text.isNotEmpty() && spinner.visibility != View.VISIBLE
    }

    private fun submit() {
        submit.hideKeyboard()
        error.visibility = View.GONE
        if (!email.text.contains('@')) {
            showError("Enter a valid email")
            return
        }
        spinner.visibility = View.VISIBLE
        submit.isEnabled = false
        later(SIGN_IN_MS) {
            spinner.visibility = View.GONE
            updateSubmit()
            if (password.text.toString() == "wrong") {
                showError("Wrong password")
            } else {
                // No "Save password?" sheet over the bench (as on Login): drop the autofill session.
                getSystemService(android.view.autofill.AutofillManager::class.java)?.cancel()
                show(otp)
                boxes[0].requestFocus()
            }
        }
    }

    private fun showError(message: String) {
        error.text = message
        error.visibility = View.VISIBLE
    }

    private fun buildOtp() {
        otp = content.column { }
        otp.text(text = "Enter the code", size = 24f, bold = true)
        otp.text(text = "We sent a 6-digit code.", secondary = true)
        val list = mutableListOf<EditText>()
        otp.row(top = 16) {
            for (i in 0 until 6) {
                list += put(AppCompatEditText(context).apply {
                    id = idOf("otp_${i + 1}")
                    inputType = InputType.TYPE_CLASS_NUMBER
                    gravity = Gravity.CENTER
                    textSize = 22f
                    importantForAutofill = View.IMPORTANT_FOR_AUTOFILL_NO
                }, w = dp(44))
            }
        }
        boxes = list
        boxes.forEachIndexed { i, box ->
            box.onTextChanged { typed -> if (!updatingBoxes) onBoxText(i, typed) }
            box.setOnKeyListener { _, keyCode, event ->
                if (keyCode == KeyEvent.KEYCODE_DEL && event.action == KeyEvent.ACTION_DOWN &&
                    box.text.isEmpty() && i > 0) {
                    boxes[i - 1].requestFocus()
                    true
                } else false
            }
        }
        otpError = otp.text(R.id.otp_error, "Wrong code").apply {
            setTextColor(color(R.color.error))
            visibility = View.GONE
        }
    }

    /** One digit per box: a digit moves on to the next box; several at once (a paste) spread out. */
    private fun onBoxText(i: Int, typed: String) {
        val digits = typed.filter { it.isDigit() }
        updatingBoxes = true
        var last = i
        if (digits.length <= 1) {
            if (digits != typed) boxes[i].setText(digits)
        } else {
            for ((k, c) in digits.withIndex()) {
                if (i + k > 5) break
                boxes[i + k].setText(c.toString())
                last = i + k
            }
        }
        updatingBoxes = false
        if (digits.isNotEmpty()) {
            otpError.visibility = View.GONE
            val next = (last + 1).coerceAtMost(5)
            boxes[next].requestFocus()
            boxes[next].setSelection(boxes[next].text.length)
        }
        val code = boxes.joinToString("") { it.text.toString() }
        if (code.length == 6) checkCode(code)
    }

    private fun checkCode(code: String) {
        if (code == "123456") {
            boxes.last().hideKeyboard()
            signedIn.text = "Signed in as ${email.text}"
            show(done)
        } else {
            otpError.visibility = View.VISIBLE
        }
        updatingBoxes = true
        boxes.forEach { it.setText("") }
        updatingBoxes = false
        boxes[0].requestFocus()
    }

    private fun buildDone() {
        done = content.column { }
        signedIn = done.text(R.id.signin_done, "", size = 20f, bold = true)
        done.button(R.id.signout, "Sign out") {
            if (!remember.isChecked) email.setText("")
            password.setText("")
            show(signIn)
        }
    }

    companion object {
        const val SIGN_IN_MS = 1000L
    }
}

/**
 * Wraps a ViewPager2 so a drag of a quarter of its width turns the page, as most pagers feel.
 * ViewPager2 alone needs half the width or a fling, so an even, unhurried drag across ~40% of
 * the page springs back. A shorter drag still springs back.
 */
class PageTurner(context: android.content.Context, private val pager: ViewPager2) : android.widget.FrameLayout(context) {
    private var downX = 0f
    private var startPage = 0

    init {
        addView(pager, LayoutParams(MATCH, MATCH))
    }

    override fun dispatchTouchEvent(e: android.view.MotionEvent): Boolean {
        when (e.actionMasked) {
            android.view.MotionEvent.ACTION_DOWN -> {
                downX = e.x
                startPage = pager.currentItem
            }
            android.view.MotionEvent.ACTION_UP -> {
                val dx = e.x - downX
                post {
                    val count = pager.adapter?.itemCount ?: 0
                    if (pager.currentItem == startPage && kotlin.math.abs(dx) > width / 4f) {
                        val target = startPage + if (dx < 0) 1 else -1
                        if (target in 0 until count) pager.currentItem = target
                    }
                }
            }
        }
        return super.dispatchTouchEvent(e)
    }
}
