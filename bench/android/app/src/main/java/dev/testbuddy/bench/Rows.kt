package dev.testbuddy.bench

import android.view.View
import android.view.ViewGroup
import androidx.recyclerview.widget.RecyclerView

/** A minimal RecyclerView adapter: [make] builds a row view, [bind] fills it. */
class Rows<T>(
    val items: MutableList<T>,
    private val make: (ViewGroup) -> View,
    private val bind: (View, T, Int) -> Unit,
) : RecyclerView.Adapter<RecyclerView.ViewHolder>() {
    override fun getItemCount() = items.size
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) = object : RecyclerView.ViewHolder(make(parent)) {}
    override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) =
        bind(holder.itemView, items[position], position)
}
