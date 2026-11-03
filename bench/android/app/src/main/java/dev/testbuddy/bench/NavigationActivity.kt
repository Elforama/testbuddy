package dev.testbuddy.bench

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.FrameLayout
import com.google.android.material.bottomnavigation.BottomNavigationView

/** Scenario 6, "Navigation and back stack": depth 0. Its first system back is intercepted. */
class NavigationActivity : ScenarioActivity() {
    private var backPresses = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.NAVIGATION.title, scroll = true)
        content.text(R.id.nav_depth, "Depth 0", size = 22f, bold = true)
        content.row {
            button(R.id.nav_push, "Push") { pushLevel(this@NavigationActivity, 1) }
            button(R.id.nav_home, "Pop to root") {} // already at the root
        }
        content.heading("Tabs with their own stacks")
        content.button(R.id.nav_tabs, "Tabbed screens") { startActivity(Intent(this, NavTabsActivity::class.java)) }
        content.heading("Dialog, then back")
        content.button(R.id.nav_dialog, "Open confirm") {
            inAppDialog(R.id.leave_dialog, "Leave?",
                Triple(R.id.leave_stay, "Stay") {},
                Triple(R.id.leave_go, "Leave") { finish() },
            ).show()
        }
        content.heading("Unsaved changes")
        content.button(R.id.nav_form, "Edit form") { startActivity(Intent(this, NavFormActivity::class.java)) }

        content.heading("System back")
        val count = content.text(R.id.back_presses, "Back presses: 0")
        val hint = content.text(R.id.back_hint, "Press back again to leave", secondary = true).apply {
            visibility = View.GONE
        }
        // The first system back is intercepted and counted; the second leaves.
        interceptBack { cb ->
            backPresses++
            count.text = "Back presses: $backPresses"
            hint.visibility = View.VISIBLE
            cb.isEnabled = false
        }
    }

    /** The header's Back leaves at once: only system back presses are intercepted. */
    override fun onBackButton() = finish()

    companion object {
        const val MAX_DEPTH = 5
        const val EXTRA_DEPTH = "depth"

        fun pushLevel(from: Activity, depth: Int) {
            from.startActivity(Intent(from, NavLevelActivity::class.java).putExtra(EXTRA_DEPTH, depth))
        }
    }
}

/** "Level N": a pushed screen with Push / Pop / Pop to root, up to depth 5. */
class NavLevelActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val depth = intent.getIntExtra(NavigationActivity.EXTRA_DEPTH, 1)
        screen("Level $depth", titleId = R.id.level_title)
        content.text(R.id.nav_depth, "Depth $depth", size = 22f, bold = true)
        content.button(R.id.nav_push, "Push") { NavigationActivity.pushLevel(this, depth + 1) }
            .isEnabled = depth < NavigationActivity.MAX_DEPTH
        content.button(R.id.nav_pop, "Pop") { finish() }
        content.button(R.id.nav_home, "Pop to root") {
            startActivity(Intent(this, NavigationActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP))
        }
    }
}

/** A bottom tab bar (Feed, Profile) where each tab keeps its own stack. */
class NavTabsActivity : ScenarioActivity() {
    private lateinit var feed: FrameLayout
    private lateinit var profile: FrameLayout
    private var post: View? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Tabbed screens", titleId = R.id.screen_title)
        content.button(R.id.tabs_close, "Close tabs") { finish() }
        val pages = content.put(FrameLayout(this), h = 0, weight = 1f)
        feed = FrameLayout(this)
        profile = FrameLayout(this).apply { visibility = View.GONE }
        pages.addView(feed, FrameLayout.LayoutParams(MATCH, MATCH))
        pages.addView(profile, FrameLayout.LayoutParams(MATCH, MATCH))

        feed.addView(panel(this) {
            text(R.id.feed_title, "Feed", size = 22f, bold = true)
            button(R.id.open_post, "Open post") { openPost() }
        })
        profile.addView(panel(this) { text(R.id.profile_title, "Profile", size = 22f, bold = true) })

        val bar = content.put(BottomNavigationView(this), top = 0)
        bar.menu.add(0, R.id.navtab_feed, 0, "Feed").setIcon(android.R.drawable.ic_menu_agenda)
        bar.menu.add(0, R.id.navtab_profile, 1, "Profile").setIcon(android.R.drawable.ic_menu_myplaces)
        bar.setOnItemSelectedListener { item ->
            feed.visibility = if (item.itemId == R.id.navtab_feed) View.VISIBLE else View.GONE
            profile.visibility = if (item.itemId == R.id.navtab_profile) View.VISIBLE else View.GONE
            true
        }
        // System back pops the Feed's own stack first.
        interceptBack { cb ->
            val p = post
            if (p != null && feed.visibility == View.VISIBLE) {
                feed.removeView(p)
                post = null
            } else {
                cb.isEnabled = false
                onBackPressedDispatcher.onBackPressed()
                cb.isEnabled = true
            }
        }
    }

    private fun openPost() {
        if (post != null) return
        post = panel(this) {
            setBackgroundColor(color(R.color.background))
            text(R.id.post_title, "Post", size = 22f, bold = true)
            text(text = "A post inside the Feed tab's own stack.", secondary = true)
            button(R.id.post_back, "Back to feed", flat = true) { feed.removeView(this@panel); post = null }
        }.also { feed.addView(it, FrameLayout.LayoutParams(MATCH, MATCH)) }
    }

    private fun panel(ctx: android.content.Context, block: android.widget.LinearLayout.() -> Unit) =
        android.widget.LinearLayout(ctx).apply {
            orientation = android.widget.LinearLayout.VERTICAL
            isClickable = true
            block()
        }
}

/** A form that asks before throwing away unsaved text. */
class NavFormActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("Edit form", titleId = R.id.screen_title)
        val field = content.field(R.id.form_field, "Title")
        interceptBack {
            if (field.text.isNullOrEmpty()) {
                finish()
            } else {
                field.hideKeyboard()
                inAppDialog(R.id.discard_dialog, "Discard changes?",
                    Triple(R.id.discard_keep, "Keep editing") {},
                    Triple(R.id.discard_yes, "Discard") { finish() },
                ).show()
            }
        }
    }
}

/** bench://level/N: the Navigation scenario at Level N with the stack 0, 1, ..., N behind it. */
class DeepLinkActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val uri = intent.data
        val depth = if (uri?.host == "level") uri.lastPathSegment?.toIntOrNull() ?: 0 else 0
        val stack = mutableListOf(
            Intent(this, ScenarioListActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK),
            Intent(this, NavigationActivity::class.java),
        )
        for (d in 1..depth.coerceIn(0, NavigationActivity.MAX_DEPTH)) {
            stack += Intent(this, NavLevelActivity::class.java).putExtra(NavigationActivity.EXTRA_DEPTH, d)
        }
        startActivities(stack.toTypedArray())
        finish()
    }
}
