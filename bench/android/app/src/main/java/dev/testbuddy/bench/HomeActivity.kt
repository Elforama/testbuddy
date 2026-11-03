package dev.testbuddy.bench

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView

class HomeActivity : Activity() {
    private val handler = Handler(Looper.getMainLooper())
    private lateinit var cartCount: TextView
    private lateinit var ticker: TextView
    private lateinit var rows: LinearLayout
    private var ticks = 0
    private var nextTick = 0L
    private var lastItem = 0

    /** "Live: N", N += 1 every 100 ms for as long as Home exists. */
    private val tick = object : Runnable {
        override fun run() {
            ticks++
            ticker.text = getString(R.string.ticker, ticks)
            nextTick += TICKER_INTERVAL_MS
            handler.postAtTime(this, nextTick)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_home)
        findViewById<ViewGroup>(android.R.id.content).getChildAt(0).padForSystemBars()

        val username = intent.getStringExtra(EXTRA_USERNAME) ?: ""
        findViewById<TextView>(R.id.welcome).text = getString(R.string.welcome, username)
        cartCount = findViewById(R.id.cart_count)
        ticker = findViewById(R.id.ticker)
        ticker.text = getString(R.string.ticker, ticks)
        rows = findViewById<ScrollView>(R.id.items).getChildAt(0) as LinearLayout

        addRows(INITIAL_ROWS)

        val loadMore = findViewById<View>(R.id.load_more)
        val loadMoreSpinner = findViewById<View>(R.id.load_more_spinner)
        loadMore.setOnClickListener {
            loadMore.visibility = View.GONE
            loadMoreSpinner.visibility = View.VISIBLE
            handler.postDelayed({
                addRows(PAGE_ROWS)
                loadMoreSpinner.visibility = View.GONE
                loadMore.visibility = View.VISIBLE
            }, LOAD_MORE_DELAY_MS)
        }

        findViewById<View>(R.id.open_live).setOnClickListener {
            startActivity(Intent(this, LiveActivity::class.java))
        }
        findViewById<View>(R.id.logout).setOnClickListener {
            startActivity(Intent(this, LoginActivity::class.java))
            finish()
        }

        nextTick = SystemClock.uptimeMillis() + TICKER_INTERVAL_MS
        handler.postAtTime(tick, nextTick)
    }

    override fun onResume() {
        super.onResume()
        cartCount.text = getString(R.string.cart_count, AppState.cart)
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        super.onDestroy()
    }

    /** Appends [count] rows before the Load more footer, which stays the last child. */
    private fun addRows(count: Int) {
        val inflater = layoutInflater
        repeat(count) {
            val n = ++lastItem
            val row = inflater.inflate(R.layout.item_row, rows, false) as TextView
            row.text = getString(R.string.item, n)
            row.setOnClickListener {
                startActivity(Intent(this, DetailActivity::class.java).putExtra(EXTRA_ITEM, n))
            }
            rows.addView(row, rows.childCount - 1)
        }
    }

    companion object {
        const val TICKER_INTERVAL_MS = 100L
        const val LOAD_MORE_DELAY_MS = 800L
        const val INITIAL_ROWS = 40
        const val PAGE_ROWS = 10
    }
}
