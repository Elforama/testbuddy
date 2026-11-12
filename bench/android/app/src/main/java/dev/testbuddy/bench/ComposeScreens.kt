package dev.testbuddy.bench

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.systemBars
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Jetpack Compose screens. Ids are test tags, exposed as resource ids (testTagsAsResourceId),
// so `id: back` matches a Compose node just like an android:id.

private val Accent = Color(0xFF3D5AFE)

/** Theme, safe-area padding and testTagsAsResourceId for a whole Compose screen. */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun BenchScreen(title: String, titleTag: String, onBack: () -> Unit, content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = lightColorScheme(primary = Accent)) {
        Column(
            Modifier
                .fillMaxSize()
                .background(Color.White)
                .semantics { testTagsAsResourceId = true }
                .windowInsetsPadding(WindowInsets.safeDrawing),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(4.dp)) {
                TextButton(onClick = onBack, modifier = Modifier.testTag("back")) { Text("Back", fontSize = 16.sp) }
                Text(title, fontSize = 20.sp, fontWeight = FontWeight.Bold, modifier = Modifier.testTag(titleTag))
            }
            Column(Modifier.padding(horizontal = 16.dp)) { content() }
        }
    }
}

/** Lists: 500 rows with sticky section headers every 25 rows (LazyColumn stickyHeader). */
class LongListActivity : ComponentActivity() {
    @OptIn(ExperimentalFoundationApi::class)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            var picked by remember { mutableStateOf("Picked nothing") }
            BenchScreen("Long list", "screen_title", onBack = { finish() }) {
                Text(picked, fontSize = 18.sp, fontWeight = FontWeight.Bold, modifier = Modifier.testTag("picked"))
                // A row's height of space: a row scrolled up past the list's top keeps its whole
                // (unclipped) bounds in Compose's accessibility tree, and Compose leaves out a
                // node such bounds cover, so "picked" right above the list would vanish.
                Spacer(Modifier.height(64.dp))
                LazyColumn(Modifier.fillMaxSize().clipToBounds().testTag("long_list")) {
                    for (section in 0 until 20) {
                        val letter = 'A' + section
                        stickyHeader(key = "section_$letter") {
                            Text(
                                "Section $letter",
                                fontWeight = FontWeight.Bold,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .background(Color(0xFFF5F6FA))
                                    .padding(horizontal = 8.dp, vertical = 6.dp)
                                    .testTag("section_$letter"),
                            )
                        }
                        items((section * 25 + 1..section * 25 + 25).toList(), key = { it }) { n ->
                            Text(
                                "Row $n",
                                fontSize = 18.sp,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .height(56.dp)
                                    .clickable { picked = "Picked Row $n" }
                                    .padding(horizontal = 8.dp, vertical = 16.dp)
                                    .testTag("row_$n"),
                            )
                        }
                    }
                }
            }
        }
    }
}

/** Native toolkits: the mini screen in Jetpack Compose (LazyColumn). */
class ToolkitComposeActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            var taps by remember { mutableIntStateOf(0) }
            var on by remember { mutableStateOf(false) }
            var typed by remember { mutableStateOf("") }
            var notes by remember { mutableStateOf("") }
            BenchScreen("Jetpack Compose", "tk_title", onBack = { finish() }) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Button(onClick = { taps++ }, modifier = Modifier.testTag("tk_tap")) { Text("Tap") }
                    Text("Taps: $taps", fontSize = 16.sp, modifier = Modifier.testTag("tk_count"))
                }
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Switch(checked = on, onCheckedChange = { on = it }, modifier = Modifier.testTag("tk_switch"))
                    Text(if (on) "On" else "Off", fontSize = 16.sp, modifier = Modifier.testTag("tk_switch_status"))
                }
                OutlinedTextField(
                    value = typed,
                    onValueChange = { typed = it },
                    placeholder = { Text("Type here") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().testTag("tk_field"),
                )
                Text("Echo: $typed", fontSize = 16.sp, modifier = Modifier.padding(vertical = 8.dp).testTag("tk_echo"))
                // A multi-line field (singleLine = false): Enter types a new line (issue #32).
                OutlinedTextField(
                    value = notes,
                    onValueChange = { notes = it },
                    placeholder = { Text("Notes") },
                    minLines = 2,
                    maxLines = 3,
                    modifier = Modifier.fillMaxWidth().testTag("tk_notes"),
                )
                Text("Notes lines: ${if (notes.isEmpty()) 0 else notes.split('\n').size}", fontSize = 16.sp,
                    modifier = Modifier.padding(vertical = 8.dp).testTag("tk_notes_lines"))
                LazyColumn(Modifier.fillMaxSize().testTag("tk_list")) {
                    items((1..30).toList()) { n ->
                        Row(Modifier.fillMaxWidth().height(56.dp), verticalAlignment = Alignment.CenterVertically) {
                            Spacer(Modifier.width(8.dp))
                            Text("Toolkit row $n", fontSize = 18.sp, modifier = Modifier.testTag("tk_row_$n"))
                        }
                    }
                }
            }
        }
    }
}

/**
 * Lists: a sideways shelf low on a Compose page that does not make room for the keyboard
 * (adjustNothing, and no imePadding). The shelf's top is at 58% of the screen's height: with
 * a phone keyboard up (its top at about 60-65%), only a strip of the shelf shows above it, and
 * its cards' labels are under it. The page scrolls far enough below the shelf to bring it up
 * above the keyboard (issue #193). A shelf all under the keyboard is not listed at all.
 */
class KeyboardShelfActivity : ComponentActivity() {
    @OptIn(ExperimentalComposeUiApi::class)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val screenHeight = resources.displayMetrics.heightPixels
        setContent {
            var picked by remember { mutableStateOf("Picked nothing") }
            var typed by remember { mutableStateOf("") }
            // The space above the shelf, set once from where the field ends on screen.
            var gap by remember { mutableIntStateOf(-1) }
            val density = LocalDensity.current
            MaterialTheme(colorScheme = lightColorScheme(primary = Accent)) {
                Column(
                    Modifier
                        .fillMaxSize()
                        .background(Color.White)
                        .semantics { testTagsAsResourceId = true }
                        .windowInsetsPadding(WindowInsets.systemBars), // not the keyboard's
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(4.dp)) {
                        TextButton(onClick = { finish() }, modifier = Modifier.testTag("back")) { Text("Back", fontSize = 16.sp) }
                        Text("Shelf under keyboard", fontSize = 20.sp, fontWeight = FontWeight.Bold,
                            modifier = Modifier.testTag("screen_title"))
                    }
                    Text(picked, fontSize = 18.sp, fontWeight = FontWeight.Bold,
                        modifier = Modifier.padding(horizontal = 16.dp).testTag("kb_shelf_picked"))
                    Column(
                        Modifier
                            .fillMaxSize()
                            .verticalScroll(rememberScrollState())
                            .padding(horizontal = 16.dp)
                            .testTag("kb_shelf_page"),
                    ) {
                        OutlinedTextField(
                            value = typed, onValueChange = { typed = it }, singleLine = true,
                            placeholder = { Text("Shelf field") },
                            modifier = Modifier
                                .fillMaxWidth()
                                .testTag("kb_shelf_field")
                                .onGloballyPositioned {
                                    if (gap < 0) {
                                        val bottom = it.positionInWindow().y + it.size.height
                                        gap = maxOf(0, (screenHeight * 0.58f - bottom).toInt())
                                    }
                                },
                        )
                        Spacer(Modifier.height(with(density) { maxOf(gap, 0).toDp() }))
                        LazyRow(
                            Modifier.fillMaxWidth().height(120.dp).testTag("kb_shelf"),
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            items((1..15).toList(), key = { it }) { n ->
                                Box(
                                    Modifier
                                        .width(140.dp)
                                        .fillMaxHeight()
                                        .background(Color(0xFFF5F6FA), RoundedCornerShape(12.dp))
                                        .clickable { picked = "Picked Shelf card $n" }
                                        .testTag("kb_shelf_card_$n"),
                                    contentAlignment = Alignment.Center,
                                ) { Text("Shelf card $n", fontSize = 16.sp) }
                            }
                        }
                        // Room below the shelf: the page can scroll it up above the keyboard.
                        Spacer(Modifier.height(with(density) { screenHeight.toDp() }))
                    }
                }
            }
        }
    }
}
