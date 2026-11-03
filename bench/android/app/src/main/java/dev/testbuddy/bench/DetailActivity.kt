package dev.testbuddy.bench

import android.app.Activity
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.widget.TextView

class DetailActivity : Activity() {
    private val handler = Handler(Looper.getMainLooper())

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_detail)
        findViewById<ViewGroup>(android.R.id.content).getChildAt(0).padForSystemBars()

        val item = intent.getIntExtra(EXTRA_ITEM, 1)
        findViewById<TextView>(R.id.detail_title).text = getString(R.string.detail_title, item)
        findViewById<TextView>(R.id.detail_text).text = getString(R.string.detail_text, item)

        val inCart = findViewById<TextView>(R.id.detail_cart)
        inCart.text = getString(R.string.in_cart, AppState.cart)
        findViewById<View>(R.id.add_to_cart).setOnClickListener {
            AppState.cart++
            inCart.text = getString(R.string.in_cart, AppState.cart)
        }

        val spinner = findViewById<View>(R.id.reviews_spinner)
        val reviews = findViewById<View>(R.id.reviews)
        handler.postDelayed({
            spinner.visibility = View.GONE
            reviews.visibility = View.VISIBLE
        }, REVIEWS_DELAY_MS)

        findViewById<View>(R.id.back).setOnClickListener { finish() }
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    companion object {
        const val REVIEWS_DELAY_MS = 600L
    }
}
