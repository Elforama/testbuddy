package dev.testbuddy.bench

import android.app.DatePickerDialog
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.SeekBar
import android.widget.TextView
import androidx.appcompat.widget.AppCompatCheckBox
import androidx.appcompat.widget.AppCompatRadioButton
import androidx.appcompat.widget.AppCompatSeekBar
import androidx.appcompat.widget.AppCompatSpinner
import android.widget.RadioGroup
import com.google.android.material.button.MaterialButtonToggleGroup
import com.google.android.material.chip.Chip
import com.google.android.material.chip.ChipGroup
import com.google.android.material.materialswitch.MaterialSwitch
import com.google.android.material.tabs.TabLayout
import java.util.Locale

/** Scenario 5, "Controls": every standard control, each with a status line. The page scrolls. */
class ControlsActivity : ScenarioActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.CONTROLS.title, scroll = true)
        tabs()
        segmented()
        radios()
        checkboxes()
        switch()
        slider()
        stepper()
        dropdown()
        datePicker()
        chips()
        rating()
        textFields()
        search()
        progress()
        expandable()
        disabled()
        longPress()
        doubleTap()
    }

    private fun tabs() {
        content.heading("Tabs")
        val status = TextView(this)
        val tabs = content.put(TabLayout(this))
        for ((id, label) in listOf(R.id.tab_one to "One", R.id.tab_two to "Two", R.id.tab_three to "Three")) {
            val tab = tabs.newTab().setText(label)
            tabs.addTab(tab)
            tab.view.id = id
        }
        tabs.addOnTabSelectedListener(object : TabLayout.OnTabSelectedListener {
            override fun onTabSelected(tab: TabLayout.Tab) { status.text = "Tab: ${tab.text}" }
            override fun onTabUnselected(tab: TabLayout.Tab) {}
            override fun onTabReselected(tab: TabLayout.Tab) {}
        })
        content.put(status.styled(R.id.tabs_status, "Tab: One"))
    }

    private fun segmented() {
        content.heading("Segmented control")
        val group = content.put(MaterialButtonToggleGroup(this).apply {
            isSingleSelection = true
            isSelectionRequired = true
        }, w = WRAP)
        val ids = listOf(R.id.seg_s to "S", R.id.seg_m to "M", R.id.seg_l to "L")
        for ((id, label) in ids) {
            group.addView(materialButton(id, label).apply {
                // Outlined toggle buttons, as in a segmented button.
                setBackgroundColor(android.graphics.Color.TRANSPARENT)
                strokeWidth = dp(1)
                setStrokeColorResource(R.color.accent)
                setTextColor(color(R.color.accent))
                isCheckable = true
            })
        }
        val status = content.text(R.id.seg_status, "Size: M")
        group.check(R.id.seg_m)
        group.addOnButtonCheckedListener { _, id, checked ->
            if (checked) status.text = "Size: ${ids.first { it.first == id }.second}"
        }
        // Checked segments are filled.
        fun paint() = ids.forEach { (id, _) ->
            val b = group.findViewById<com.google.android.material.button.MaterialButton>(id)
            b.setBackgroundColor(if (b.isChecked) color(R.color.accent) else android.graphics.Color.TRANSPARENT)
            b.setTextColor(if (b.isChecked) android.graphics.Color.WHITE else color(R.color.accent))
        }
        paint()
        group.addOnButtonCheckedListener { _, _, _ -> paint() }
    }

    private fun radios() {
        content.heading("Radio group")
        val status = TextView(this)
        val group = content.put(RadioGroup(this).apply { orientation = RadioGroup.HORIZONTAL })
        for ((id, label) in listOf(R.id.radio_red to "Red", R.id.radio_green to "Green", R.id.radio_blue to "Blue")) {
            group.addView(AppCompatRadioButton(this).apply { this.id = id; text = label })
        }
        group.setOnCheckedChangeListener { g, id -> status.text = "Color: ${g.findViewById<TextView>(id).text}" }
        content.put(status.styled(R.id.radio_status, "Color: none"))
    }

    private fun checkboxes() {
        content.heading("Checkboxes")
        val status = TextView(this)
        val boxes = mutableListOf<AppCompatCheckBox>()
        content.row {
            for ((id, label) in listOf(R.id.check_milk to "Milk", R.id.check_eggs to "Eggs")) {
                boxes += put(AppCompatCheckBox(context).apply { this.id = id; text = label })
            }
        }
        boxes.forEach { box ->
            box.setOnCheckedChangeListener { _, _ ->
                val picked = boxes.filter { it.isChecked }.joinToString(", ") { it.text }
                status.text = "Basket: ${picked.ifEmpty { "empty" }}"
            }
        }
        content.put(status.styled(R.id.check_status, "Basket: empty"))
    }

    private fun switch() {
        content.heading("Switch")
        val status = TextView(this)
        content.put(MaterialSwitch(this).apply {
            id = R.id.switch_wifi
            text = "Wi-Fi"
            setOnCheckedChangeListener { _, on -> status.text = if (on) "Wi-Fi on" else "Wi-Fi off" }
        })
        content.put(status.styled(R.id.switch_status, "Wi-Fi off"))
    }

    private fun slider() {
        content.heading("Slider")
        val status = TextView(this)
        content.put(AppCompatSeekBar(this).apply {
            id = R.id.slider_volume
            contentDescription = "Volume"
            max = 100
            progress = 50
            setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
                override fun onProgressChanged(s: SeekBar, p: Int, fromUser: Boolean) { status.text = "Volume: $p" }
                override fun onStartTrackingTouch(s: SeekBar) {}
                override fun onStopTrackingTouch(s: SeekBar) {}
            })
        }, h = dp(48))
        content.put(status.styled(R.id.slider_status, "Volume: 50"))
    }

    private fun stepper() {
        content.heading("Stepper")
        var quantity = 1
        lateinit var status: TextView
        content.row {
            button(R.id.stepper_minus, "−") {
                quantity = (quantity - 1).coerceAtLeast(0); status.text = "Quantity: $quantity"
            }
            button(R.id.stepper_plus, "+") {
                quantity = (quantity + 1).coerceAtMost(10); status.text = "Quantity: $quantity"
            }
        }
        status = content.text(R.id.stepper_status, "Quantity: 1")
    }

    private fun dropdown() {
        content.heading("Dropdown")
        val status = TextView(this)
        val fruits = listOf("Apple", "Banana", "Cherry")
        content.put(AppCompatSpinner(this, android.widget.Spinner.MODE_DROPDOWN).apply {
            id = R.id.dropdown_fruit
            adapter = ArrayAdapter(context, android.R.layout.simple_spinner_item, fruits).apply {
                setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item)
            }
            onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
                override fun onItemSelected(p: AdapterView<*>?, v: View?, pos: Int, id: Long) {
                    status.text = "Fruit: ${fruits[pos]}"
                }
                override fun onNothingSelected(p: AdapterView<*>?) {}
            }
        }, w = dp(200), h = dp(48))
        content.put(status.styled(R.id.dropdown_status, "Fruit: Apple"))
    }

    private fun datePicker() {
        content.heading("Date picker")
        val status = TextView(this)
        content.button(R.id.date_open, "Pick a date") {
            // A fixed start date keeps the dialog deterministic.
            DatePickerDialog(this, { _, y, m, d ->
                status.text = String.format(Locale.US, "Date: %04d-%02d-%02d", y, m + 1, d)
            }, 2026, 5, 15).show()
        }
        content.put(status.styled(R.id.date_status, "Date: none"))
    }

    private fun chips() {
        content.heading("Chips")
        val status = TextView(this)
        val group = content.put(ChipGroup(this))
        val chips = listOf(R.id.chip_fast to "Fast", R.id.chip_cheap to "Cheap", R.id.chip_good to "Good").map { (id, label) ->
            Chip(this, null, com.google.android.material.R.attr.chipStyle).apply {
                this.id = id
                text = label
                isCheckable = true
                group.addView(this)
            }
        }
        chips.forEach { chip ->
            chip.setOnCheckedChangeListener { _, _ ->
                val on = chips.filter { it.isChecked }.joinToString(", ") { it.text }
                status.text = "Filters: ${on.ifEmpty { "none" }}"
            }
        }
        content.put(status.styled(R.id.chip_status, "Filters: none"))
    }

    private fun rating() {
        content.heading("Rating")
        lateinit var status: TextView
        val stars = mutableListOf<TextView>()
        content.row {
            for (k in 1..5) {
                stars += put(TextView(context).apply {
                    id = idOf("star_$k")
                    text = "☆"
                    textSize = 32f
                    contentDescription = "$k star" + if (k > 1) "s" else ""
                    setTextColor(color(R.color.accent))
                    isClickable = true
                    setOnClickListener {
                        stars.forEachIndexed { i, s -> s.text = if (i < k) "★" else "☆" }
                        status.text = "Rating: $k"
                    }
                })
            }
        }
        status = content.text(R.id.rating_status, "Rating: 0")
    }

    private fun textFields() {
        content.heading("Text fields")
        lateinit var name: TextView
        lateinit var age: TextView
        lateinit var secret: TextView
        lateinit var notes: TextView
        content.field(R.id.field_name, "Your name") { name.text = "Hello, ${it.ifEmpty { "nobody" }}" }
        name = content.text(R.id.field_status, "Hello, nobody")
        content.field(R.id.field_age, "Age", InputType.TYPE_CLASS_NUMBER) { age.text = "Age: ${it.ifEmpty { "none" }}" }
        age = content.text(R.id.age_status, "Age: none")
        content.field(R.id.field_secret, "Secret", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD) {
            secret.text = "Secret length: ${it.length}"
        }.typeface = android.graphics.Typeface.DEFAULT
        secret = content.text(R.id.secret_status, "Secret length: 0")
        content.field(R.id.field_notes, "Notes", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE) {
            notes.text = "Lines: ${if (it.isEmpty()) 0 else it.split('\n').size}"
        }.apply { minLines = 3; maxLines = 3; gravity = Gravity.TOP or Gravity.START }
        notes = content.text(R.id.notes_status, "Lines: 0")
    }

    private fun search() {
        content.heading("Search")
        val fruits = listOf("Apple", "Apricot", "Banana", "Blueberry", "Cherry")
        lateinit var results: LinearLayout
        content.field(R.id.field_search, "Search fruit") { q ->
            results.removeAllViews()
            fruits.filter { it.startsWith(q.trim(), ignoreCase = true) }.forEach { f ->
                results.text(idOf("search_${f.lowercase()}"), f)
            }
        }
        results = content.column { id = R.id.search_results }
        fruits.forEach { results.text(idOf("search_${it.lowercase()}"), it) }
    }

    private fun progress() {
        content.heading("Progress")
        val bar = content.put(ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            id = R.id.progress_bar
            max = 100
            progress = 0
        })
        val status = content.text(R.id.progress_status, "Progress: 0%")
        var running = false
        content.button(R.id.progress_go, "Start") {
            if (running) return@button
            running = true
            bar.progress = 0
            status.text = "Progress: 0%"
            for (step in 1..10) {
                later(step * 200L) {
                    bar.progress = step * 10
                    status.text = "Progress: ${step * 10}%"
                    if (step == 10) running = false
                }
            }
        }
    }

    private fun expandable() {
        content.heading("Expandable section")
        lateinit var section: LinearLayout
        content.button(R.id.expand_toggle, "More options") {
            section.visibility = if (section.visibility == View.VISIBLE) View.GONE else View.VISIBLE
        }
        section = content.column {
            id = R.id.expand_content
            visibility = View.GONE
            text(text = "Hidden option", bold = true)
            val status = TextView(context)
            put(MaterialSwitch(context).apply {
                id = R.id.switch_hidden
                text = "Hidden switch"
                setOnCheckedChangeListener { _, on -> status.text = if (on) "Hidden on" else "Hidden off" }
            })
            put(status.styled(R.id.hidden_status, "Hidden off"))
        }
    }

    private fun disabled() {
        content.heading("Disabled button")
        content.button(R.id.disabled_button, "Can't tap me") {}.isEnabled = false
        content.text(R.id.disabled_status, "Untouched")
    }

    private fun longPress() {
        content.heading("Long press")
        lateinit var status: TextView
        content.button(R.id.longpress_button, "Hold me") { status.text = "Just tapped" }
            .setOnLongClickListener { status.text = "Held"; true }
        status = content.text(R.id.longpress_status, "Not held")
    }

    private fun doubleTap() {
        content.heading("Double tap")
        lateinit var status: TextView
        var taps = 0
        val decide = Runnable {
            if (taps == 1) status.text = "Single tap"
            taps = 0
        }
        content.put(TextView(this).apply {
            id = R.id.doubletap_box
            text = "Double-tap me"
            textSize = 18f
            gravity = Gravity.CENTER
            setTextColor(color(R.color.text))
            background = rounded(color(R.color.surface))
            setOnClickListener {
                taps++
                handler.removeCallbacks(decide)
                if (taps >= 2) {
                    status.text = "Double-tapped"
                    taps = 0
                } else {
                    handler.postDelayed(decide, DOUBLE_TAP_MS)
                }
            }
        }, h = dp(96))
        status = content.text(R.id.doubletap_status, "No taps")
    }

    private fun TextView.styled(id: Int, text: String): TextView = apply {
        this.id = id
        this.text = text
        textSize = 16f
        setTextColor(color(R.color.text))
    }

    companion object {
        const val DOUBLE_TAP_MS = 300L
    }
}
