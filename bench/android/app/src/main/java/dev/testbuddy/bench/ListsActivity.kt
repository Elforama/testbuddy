package dev.testbuddy.bench

import android.content.Intent
import android.graphics.Canvas
import android.os.Bundle
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.recyclerview.widget.GridLayoutManager
import androidx.recyclerview.widget.ItemTouchHelper
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout

/** Scenario 8, "Lists": each button opens one kind of list. */
class ListsActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.LISTS.title, scroll = true)
        content.button(R.id.open_long_list, "Long list", w = MATCH) {
            startActivity(Intent(this, LongListActivity::class.java))
        }
        for ((id, kind) in listOf(
            R.id.open_grid to "Grid",
            R.id.open_carousels to "Carousels",
            R.id.open_two_pane to "Two panes",
            R.id.open_refresh to "Pull to refresh",
            R.id.open_swipe_delete to "Swipe to delete",
            R.id.open_reorder to "Reorder",
            R.id.open_infinite to "Endless feed",
            R.id.open_empty to "Empty list",
        )) {
            content.button(id, kind, w = MATCH) {
                startActivity(Intent(this, ListDemoActivity::class.java).putExtra(EXTRA_KIND, kind))
            }
        }
    }

    companion object {
        const val EXTRA_KIND = "kind"
    }
}

/** One list screen of the Lists scenario, picked by its button's text. */
class ListDemoActivity : ScenarioActivity() {
    private lateinit var picked: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val kind = intent.getStringExtra(ListsActivity.EXTRA_KIND) ?: "Grid"
        screen(kind, titleId = R.id.screen_title)
        when (kind) {
            "Grid" -> grid()
            "Carousels" -> carousels()
            "Two panes" -> twoPanes()
            "Pull to refresh" -> pullToRefresh()
            "Swipe to delete" -> swipeToDelete()
            "Reorder" -> reorder()
            "Endless feed" -> endless()
            "Empty list" -> empty()
        }
    }

    private fun pickedLine() {
        picked = content.text(R.id.picked, "Picked nothing", size = 18f, bold = true)
    }

    private fun recycler(id: Int = View.NO_ID): RecyclerView =
        content.put(RecyclerView(this).apply { if (id != View.NO_ID) this.id = id }, h = 0, weight = 1f)

    /** A 56 dp tappable text row. */
    private fun rowView(parent: ViewGroup, height: Int = 56): TextView = TextView(parent.context).apply {
        layoutParams = ViewGroup.LayoutParams(MATCH, dp(height))
        gravity = Gravity.CENTER_VERTICAL
        textSize = 18f
        setTextColor(color(R.color.text))
        setPadding(dp(8), 0, dp(8), 0)
        setBackgroundResource(android.R.drawable.list_selector_background)
        isClickable = true
    }

    private fun grid() {
        pickedLine()
        val list = recycler(R.id.grid)
        list.layoutManager = GridLayoutManager(this, 3)
        list.adapter = Rows((1..120).toMutableList(), { parent ->
            val size = parent.width / 3
            TextView(parent.context).apply {
                layoutParams = ViewGroup.LayoutParams(MATCH, if (size > 0) size else dp(120))
                gravity = Gravity.CENTER
                textSize = 16f
                setTextColor(color(R.color.text))
                background = rounded(color(R.color.surface), 8)
                isClickable = true
            }
        }) { v, n, _ ->
            v as TextView
            v.id = idOf("cell_$n")
            v.text = "Cell $n"
            v.setOnClickListener { picked.text = "Picked Cell $n" }
        }
    }

    private fun carousels() {
        pickedLine()
        val list = recycler(R.id.shelves)
        list.layoutManager = LinearLayoutManager(this)
        val pool = RecyclerView.RecycledViewPool()
        list.adapter = Rows((1..10).toMutableList(), { parent ->
            LinearLayout(parent.context).apply {
                orientation = LinearLayout.VERTICAL
                layoutParams = ViewGroup.LayoutParams(MATCH, WRAP)
                text(size = 18f, bold = true)
                put(RecyclerView(context).apply {
                    layoutManager = LinearLayoutManager(context, LinearLayoutManager.HORIZONTAL, false)
                    setRecycledViewPool(pool)
                }, h = dp(120))
            }
        }) { v, s, _ ->
            v as LinearLayout
            (v.getChildAt(0) as TextView).apply { id = idOf("shelf_$s"); text = "Shelf $s" }
            val shelf = v.getChildAt(1) as RecyclerView
            shelf.id = idOf("shelf_${s}_cards")
            shelf.adapter = Rows((1..15).toMutableList(), { parent ->
                TextView(parent.context).apply {
                    layoutParams = ViewGroup.MarginLayoutParams(dp(140), MATCH).apply { marginEnd = dp(8) }
                    gravity = Gravity.CENTER
                    textSize = 16f
                    setTextColor(color(R.color.text))
                    background = rounded(color(R.color.surface), 12)
                    isClickable = true
                }
            }) { card, n, _ ->
                card as TextView
                card.id = idOf("card_${s}_$n")
                card.text = "Card $s-$n"
                card.setOnClickListener { picked.text = "Picked Card $s-$n" }
            }
        }
    }

    /** Master/detail side by side at any width: the list takes 40%, the mail the rest. */
    private fun twoPanes() {
        val panes = content.put(LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }, h = 0, weight = 1f)
        val list = RecyclerView(this).apply { id = R.id.mail_list; layoutManager = LinearLayoutManager(context) }
        panes.addView(list, LinearLayout.LayoutParams(0, MATCH, 0.4f))
        val detail = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(12), 0, 0, 0)
        }
        panes.addView(detail, LinearLayout.LayoutParams(0, MATCH, 0.6f))
        val title = detail.text(R.id.mail_detail, "", size = 20f, bold = true)
        val body = detail.put(ScrollView(this).apply { id = R.id.mail_body }, h = 0, weight = 1f)
        val lines = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        body.addView(lines)
        fun open(n: Int) {
            title.text = "Mail $n"
            lines.removeAllViews()
            for (k in 1..60) lines.text(text = "Line $k of mail $n")
            body.scrollTo(0, 0)
        }
        list.adapter = Rows((1..80).toMutableList(), { rowView(it) }) { v, n, _ ->
            v as TextView
            v.id = idOf("mail_$n")
            v.text = "Mail $n"
            v.setOnClickListener { open(n) }
        }
        open(1)
    }

    private fun pullToRefresh() {
        var refreshed = 0
        val count = content.text(R.id.refresh_count, "Refreshed 0 times")
        val swipe = content.put(SwipeRefreshLayout(this).apply { id = R.id.refresh_list_container }, h = 0, weight = 1f)
        // The refresh circle is SwipeRefreshLayout's first child: give it the spec's id.
        swipe.getChildAt(0)?.id = R.id.refresh_spinner
        val list = RecyclerView(this).apply { id = R.id.refresh_list; layoutManager = LinearLayoutManager(context); isNestedScrollingEnabled = false }
        swipe.addView(list)
        list.adapter = Rows((1..10).toMutableList(), { rowView(it) }) { v, n, _ -> (v as TextView).text = "Fresh $n" }
        swipe.setOnRefreshListener {
            later(REFRESH_MS) {
                swipe.isRefreshing = false
                refreshed++
                count.text = "Refreshed $refreshed times"
            }
        }
    }

    private fun swipeToDelete() {
        val status = content.text(R.id.delete_status, "Tasks: 8")
        val list = recycler(R.id.task_list)
        list.layoutManager = LinearLayoutManager(this)
        val rows = Rows((1..8).toMutableList(), { rowView(it).apply { setBackgroundColor(color(R.color.background)) } }) { v, n, _ ->
            v as TextView
            v.id = idOf("task_$n")
            v.text = "Task $n"
        }
        list.adapter = rows
        ItemTouchHelper(object : ItemTouchHelper.SimpleCallback(0, ItemTouchHelper.LEFT) {
            override fun onMove(rv: RecyclerView, a: RecyclerView.ViewHolder, b: RecyclerView.ViewHolder) = false
            // A third of the row's width is enough (the default is half).
            override fun getSwipeThreshold(holder: RecyclerView.ViewHolder) = 0.3f
            override fun onSwiped(holder: RecyclerView.ViewHolder, direction: Int) {
                val at = holder.bindingAdapterPosition
                rows.items.removeAt(at)
                rows.notifyItemRemoved(at)
                status.text = "Tasks: ${rows.items.size}"
            }
            // A red "Delete" background shows behind the row as it slides.
            override fun onChildDraw(c: Canvas, rv: RecyclerView, holder: RecyclerView.ViewHolder,
                                     dX: Float, dY: Float, state: Int, active: Boolean) {
                val v = holder.itemView
                val paint = android.graphics.Paint().apply { color = color(R.color.error) }
                c.drawRect(v.right + dX, v.top.toFloat(), v.right.toFloat(), v.bottom.toFloat(), paint)
                super.onChildDraw(c, rv, holder, dX, dY, state, active)
            }
        }).attachToRecyclerView(list)
    }

    private fun reorder() {
        val status = content.text(R.id.order_status, "Order: ABCDE")
        val list = recycler(R.id.reorder_list)
        list.layoutManager = LinearLayoutManager(this)
        lateinit var helper: ItemTouchHelper
        val rows = Rows(mutableListOf("A", "B", "C", "D", "E"), { parent ->
            LinearLayout(parent.context).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.CENTER_VERTICAL
                layoutParams = ViewGroup.LayoutParams(MATCH, dp(56))
                setBackgroundColor(color(R.color.background))
                text(size = 18f).layoutParams = LinearLayout.LayoutParams(0, WRAP, 1f)
                text(text = "≡", size = 28f, secondary = true).apply {
                    setPadding(dp(16), 0, dp(16), 0)
                    gravity = Gravity.CENTER
                }
            }
        }) { v, letter, _ ->
            v as LinearLayout
            v.id = idOf("reorder_$letter")
            (v.getChildAt(0) as TextView).text = letter
            (v.getChildAt(1) as TextView).apply {
                id = idOf("handle_$letter")
                contentDescription = "Reorder $letter"
            }
        }
        list.adapter = rows
        helper = ItemTouchHelper(object : ItemTouchHelper.SimpleCallback(ItemTouchHelper.UP or ItemTouchHelper.DOWN, 0) {
            override fun isLongPressDragEnabled() = false
            override fun onMove(rv: RecyclerView, a: RecyclerView.ViewHolder, b: RecyclerView.ViewHolder): Boolean {
                val from = a.bindingAdapterPosition
                val to = b.bindingAdapterPosition
                rows.items.add(to, rows.items.removeAt(from))
                rows.notifyItemMoved(from, to)
                status.text = "Order: ${rows.items.joinToString("")}"
                return true
            }
            override fun onSwiped(holder: RecyclerView.ViewHolder, direction: Int) {}
        })
        helper.attachToRecyclerView(list)
        // Long press on a handle starts the drag (the row itself does not).
        list.addOnChildAttachStateChangeListener(object : RecyclerView.OnChildAttachStateChangeListener {
            override fun onChildViewAttachedToWindow(view: View) {
                val handle = (view as LinearLayout).getChildAt(1)
                handle.setOnLongClickListener {
                    helper.startDrag(list.getChildViewHolder(view))
                    true
                }
            }
            override fun onChildViewDetachedFromWindow(view: View) {}
        })
    }

    private fun endless() {
        val list = recycler(R.id.feed)
        list.layoutManager = LinearLayoutManager(this)
        val loading = -1 // the spinner row
        val rows = Rows((1..20).toMutableList(), { parent ->
            FrameLayout(parent.context).apply {
                layoutParams = ViewGroup.LayoutParams(MATCH, dp(64))
                addView(rowView(this, 64))
                addView(context.spinner(R.id.feed_spinner), FrameLayout.LayoutParams(dp(36), dp(36), Gravity.CENTER))
            }
        }) { v, n, _ ->
            v as FrameLayout
            val text = v.getChildAt(0) as TextView
            val spin = v.getChildAt(1)
            text.visibility = if (n == loading) View.GONE else View.VISIBLE
            spin.visibility = if (n == loading) View.VISIBLE else View.GONE
            if (n != loading) text.text = "Post $n"
        }
        list.adapter = rows
        list.addOnScrollListener(object : RecyclerView.OnScrollListener() {
            override fun onScrolled(rv: RecyclerView, dx: Int, dy: Int) {
                val last = rows.items.lastOrNull() ?: return
                if (last == loading || last >= MAX_POSTS || rv.canScrollVertically(1)) return
                rows.items += loading
                rv.post { rows.notifyItemInserted(rows.items.size - 1) }
                later(FEED_MS) {
                    rows.items.removeAt(rows.items.size - 1)
                    val from = rows.items.size
                    rows.items += (last + 1..(last + 20).coerceAtMost(MAX_POSTS))
                    rows.notifyItemRemoved(from)
                    rows.notifyItemRangeInserted(from, rows.items.size - from)
                }
            }
        })
    }

    private fun empty() {
        val list = recycler(R.id.empty_list)
        list.layoutManager = LinearLayoutManager(this)
        val emptyText = TextView(this)
        val rows = Rows(mutableListOf<Int>(), { rowView(it) }) { v, n, _ -> (v as TextView).text = "New row $n" }
        list.adapter = rows
        // The empty state sits over the (empty) list.
        content.removeView(list)
        val frame = content.put(FrameLayout(this), h = 0, weight = 1f)
        frame.addView(list, FrameLayout.LayoutParams(MATCH, MATCH))
        val state = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
        }
        frame.addView(state, FrameLayout.LayoutParams(MATCH, MATCH))
        state.put(emptyText.apply {
            id = R.id.empty_text
            text = "Nothing here yet"
            textSize = 18f
            gravity = Gravity.CENTER
            setTextColor(color(R.color.text_secondary))
        })
        content.button(R.id.empty_add, "Add one", w = MATCH) {
            rows.items += rows.items.size + 1
            rows.notifyItemInserted(rows.items.size - 1)
            state.visibility = View.GONE
        }
    }

    companion object {
        const val REFRESH_MS = 1000L
        const val FEED_MS = 800L
        const val MAX_POSTS = 200
    }
}
