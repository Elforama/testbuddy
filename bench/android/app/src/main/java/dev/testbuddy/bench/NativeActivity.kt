package dev.testbuddy.bench

import android.content.Intent
import android.os.Bundle

/** Scenario 11, "Native toolkits": the same mini screen in Views, Compose and a WebView. */
class NativeActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.NATIVE.title)
        content.button(R.id.tk_views, "Android Views", w = MATCH) {
            startActivity(Intent(this, ToolkitViewsActivity::class.java))
        }
        content.button(R.id.tk_compose, "Jetpack Compose", w = MATCH) {
            startActivity(Intent(this, ToolkitComposeActivity::class.java))
        }
        content.button(R.id.tk_webview, "WebView", w = MATCH) {
            startActivity(Intent(this, ToolkitWebViewActivity::class.java))
        }
    }
}
