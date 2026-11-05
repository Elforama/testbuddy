package dev.testbuddy.bench

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.widget.LinearLayout
import android.widget.ScrollView
import androidx.appcompat.app.AppCompatActivity

/** The scenarios, in list order (bench/SCENARIOS.md). */
enum class Scenario(val key: String, val title: String, val screen: Class<out Activity>) {
    JOURNEY("journey", "Journey", LoginActivity::class.java),
    SLOW("slow", "Slow and janky", SlowActivity::class.java),
    PERMISSIONS("permissions", "Permissions", PermissionsActivity::class.java),
    OVERLAYS("overlays", "Overlays", OverlaysActivity::class.java),
    CONTROLS("controls", "Controls", ControlsActivity::class.java),
    NAVIGATION("navigation", "Navigation and back stack", NavigationActivity::class.java),
    ONBOARDING("onboarding", "Onboarding and login", OnboardingActivity::class.java),
    LISTS("lists", "Lists", ListsActivity::class.java),
    A11Y_POOR("a11y_poor", "Poor accessibility", PoorA11yActivity::class.java),
    A11Y_GOOD("a11y_good", "Good accessibility", GoodA11yActivity::class.java),
    NATIVE("native", "Native toolkits", NativeActivity::class.java),
    ANIMATIONS("animations", "Animations", AnimationsActivity::class.java),
    ERRORS("errors", "Errors and retries", ErrorsActivity::class.java),
    KEYBOARD("keyboard", "Keyboard", KeyboardActivity::class.java),
    TRANSITIONS("transitions", "Screen transitions", TransitionsActivity::class.java),
}

/** The start screen: one 64 dp row (a button) per scenario; on a phone some are below the fold. */
class ScenarioListActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val column = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        column.text(R.id.scenarios_title, "Scenarios", size = 28f, bold = true).apply {
            setPadding(dp(20), dp(16), dp(20), dp(8))
        }
        val list = ScrollView(this).apply { id = R.id.scenarios }
        val rows = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        for (s in Scenario.entries) {
            val b = materialButton(idOf("scenario_${s.key}"), s.title, flat = true) {
                startActivity(Intent(this, s.screen))
            }
            b.gravity = Gravity.START or Gravity.CENTER_VERTICAL
            b.textSize = 18f
            b.setTextColor(color(R.color.text))
            b.setPadding(dp(20), 0, dp(20), 0)
            b.insetTop = 0
            b.insetBottom = 0
            b.cornerRadius = 0
            rows.addView(b, LinearLayout.LayoutParams(MATCH, dp(64)))
            rows.addView(View(this).apply { setBackgroundColor(color(R.color.divider)) },
                LinearLayout.LayoutParams(MATCH, dp(1)))
        }
        list.addView(rows)
        column.addView(list, LinearLayout.LayoutParams(MATCH, 0, 1f))
        setContentView(column)
        column.padForSystemBars()
    }
}
