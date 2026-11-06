package dev.testbuddy.bench

import android.annotation.SuppressLint
import android.content.Intent
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.EditText
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.google.android.material.materialswitch.MaterialSwitch

/** The mini screen in Android Views: an XML layout and a RecyclerView. */
class ToolkitViewsActivity : AppCompatActivity() {
    private var taps = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_toolkit_views)
        findViewById<View>(R.id.tk_list).parent.let { (it as View).padForSystemBars() }
        findViewById<View>(R.id.back).setOnClickListener { finish() }
        val count = findViewById<TextView>(R.id.tk_count)
        findViewById<View>(R.id.tk_tap).setOnClickListener {
            taps++
            count.text = "Taps: $taps"
        }
        val status = findViewById<TextView>(R.id.tk_switch_status)
        findViewById<MaterialSwitch>(R.id.tk_switch).setOnCheckedChangeListener { _, on ->
            status.text = if (on) "On" else "Off"
        }
        val echo = findViewById<TextView>(R.id.tk_echo)
        findViewById<EditText>(R.id.tk_field).onTextChanged { echo.text = "Echo: $it" }
        val list = findViewById<RecyclerView>(R.id.tk_list)
        list.layoutManager = LinearLayoutManager(this)
        list.adapter = Rows((1..30).toMutableList(), { parent: ViewGroup ->
            TextView(parent.context).apply {
                layoutParams = ViewGroup.LayoutParams(MATCH, dp(56))
                gravity = Gravity.CENTER_VERTICAL
                textSize = 18f
                setTextColor(color(R.color.text))
                setPadding(dp(24), 0, dp(24), 0)
            }
        }) { v, n, _ ->
            v as TextView
            v.id = idOf("tk_row_$n")
            v.text = "Toolkit row $n"
        }
    }
}

/** The mini screen as a local HTML page (assets/toolkit.html) in a WebView, under a native Back. */
class ToolkitWebViewActivity : ScenarioActivity() {
    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen("", titleId = R.id.screen_title)
        titleView.visibility = View.GONE // the page has its own title (tk_title)
        content.setPadding(0, 0, 0, 0)
        val web = content.put(WebView(this).apply {
            id = R.id.tk_web
            settings.javaScriptEnabled = true
            settings.allowFileAccess = false
            webViewClient = WebViewClient()
        }, h = 0, weight = 1f, top = 0)
        web.loadUrl("file:///android_asset/toolkit.html")
    }
}
