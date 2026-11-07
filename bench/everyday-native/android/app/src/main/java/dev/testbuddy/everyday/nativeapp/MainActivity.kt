package dev.testbuddy.everyday.nativeapp

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTagsAsResourceId
import androidx.compose.ui.text.input.*
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { Everyday() }
    }
}

private data class Item(val id: Int, val title: String, val done: Boolean = false)

@Composable
private fun Field(
    id: String,
    title: String,
    text: String,
    change: (String) -> Unit,
    secret: Boolean = false,
) {
    val focus = LocalFocusManager.current
    OutlinedTextField(
        value = text,
        onValueChange = change,
        label = { Text(title) },
        modifier = Modifier.fillMaxWidth().testTag(id),
        singleLine = true,
        visualTransformation =
            if (secret) PasswordVisualTransformation() else VisualTransformation.None,
        keyboardOptions =
            KeyboardOptions(
                keyboardType = if (secret) KeyboardType.Password else KeyboardType.Text,
                imeAction = ImeAction.Done,
            ),
        keyboardActions = KeyboardActions(onDone = { focus.clearFocus() }),
    )
}

@Composable
private fun Check(id: String, title: String, checked: Boolean, change: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth()
            .testTag(id)
            .toggleable(value = checked, role = Role.Checkbox, onValueChange = change)
            .padding(vertical = 8.dp)
    ) {
        Checkbox(checked = checked, onCheckedChange = null)
        Text(title, modifier = Modifier.padding(start = 12.dp, top = 12.dp))
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun Everyday() {
    var signedIn by remember { mutableStateOf(false) }
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var loading by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val focus = LocalFocusManager.current
    var tab by remember { mutableIntStateOf(0) }
    var info by remember { mutableStateOf<String?>(null) }
    val drawer = rememberDrawerState(DrawerValue.Closed)
    val items = remember {
        mutableStateListOf(
            Item(1, "Buy groceries"),
            Item(2, "Water the plants"),
            Item(3, "Read a chapter"),
        )
    }
    var nextId by remember { mutableIntStateOf(4) }
    var adding by remember { mutableStateOf(false) }
    var newItem by remember { mutableStateOf("") }
    val switches = remember { mutableStateListOf(true, false, false) }
    val topics = remember { mutableStateListOf(true, false, false) }
    var frequency by remember { mutableStateOf("Daily") }
    var note by remember { mutableStateOf("") }
    var name by remember { mutableStateOf("Alex") }
    var city by remember { mutableStateOf("") }
    var terms by remember { mutableStateOf(false) }
    var settingsStatus by remember { mutableStateOf("") }
    var profileStatus by remember { mutableStateOf("") }

    fun logout() {
        signedIn = false
        email = ""
        password = ""
        tab = 0
        info = null
        items.clear()
        items.addAll(
            listOf(Item(1, "Buy groceries"), Item(2, "Water the plants"), Item(3, "Read a chapter"))
        )
        nextId = 4
        note = ""
        name = "Alex"
        city = ""
        terms = false
        frequency = "Daily"
        switches[0] = true
        switches[1] = false
        switches[2] = false
        topics[0] = true
        topics[1] = false
        topics[2] = false
        settingsStatus = ""
        profileStatus = ""
    }

    BackHandler(enabled = info != null) { info = null }

    MaterialTheme(colorScheme = lightColorScheme(primary = Color(0xff246b60))) {
        Surface(Modifier.fillMaxSize().semantics { testTagsAsResourceId = true }) {
            if (!signedIn) {
                Scaffold(
                    modifier = Modifier.imePadding(),
                    topBar = {
                        TopAppBar(title = { Text("Everyday", Modifier.testTag("login_title")) })
                    },
                ) { padding ->
                    Column(
                        Modifier.padding(padding)
                            .padding(24.dp)
                            .verticalScroll(rememberScrollState()),
                        verticalArrangement = Arrangement.spacedBy(16.dp),
                    ) {
                        Text("Welcome back", style = MaterialTheme.typography.headlineMedium)
                        Text("Use any email and password to try the demo.")
                        Field("login_email", "Email", email, { email = it })
                        Field(
                            "login_password",
                            "Password",
                            password,
                            { password = it },
                            secret = true,
                        )
                        Button(
                            onClick = {
                                focus.clearFocus()
                                loading = true
                                scope.launch {
                                    delay(500)
                                    loading = false
                                    signedIn = true
                                }
                            },
                            enabled = !loading,
                            modifier = Modifier.testTag("login_submit"),
                        ) {
                            Text(if (loading) "Signing in…" else "Sign in")
                        }
                        if (loading) CircularProgressIndicator(Modifier.testTag("login_loading"))
                    }
                }
            } else {
                ModalNavigationDrawer(
                    drawerState = drawer,
                    drawerContent = {
                        ModalDrawerSheet {
                            Text(
                                "Everyday",
                                style = MaterialTheme.typography.headlineMedium,
                                modifier = Modifier.padding(20.dp),
                            )
                            for ((i, title) in
                                listOf("My list", "Settings", "Profile").withIndex()) {
                                NavigationDrawerItem(
                                    label = { Text(title) },
                                    selected = tab == i,
                                    onClick = {
                                        tab = i
                                        scope.launch { drawer.close() }
                                    },
                                    modifier = Modifier.testTag("menu_$i"),
                                )
                            }
                            HorizontalDivider()
                            for (title in listOf("Help", "About", "Sign out")) {
                                NavigationDrawerItem(
                                    label = { Text(title) },
                                    selected = false,
                                    onClick = {
                                        scope.launch {
                                            drawer.close()
                                            if (title == "Sign out") logout() else info = title
                                        }
                                    },
                                    modifier =
                                        Modifier.testTag(
                                            when (title) {
                                                "Help" -> "menu_help"
                                                "About" -> "menu_about"
                                                else -> "menu_logout"
                                            }
                                        ),
                                )
                            }
                        }
                    },
                ) {
                    Scaffold(
                        modifier = Modifier.imePadding(),
                        topBar = {
                            TopAppBar(
                                title = {
                                    Text(
                                        info ?: listOf("My list", "Settings", "Profile")[tab],
                                        Modifier.testTag(
                                            if (info == null) "home_title" else "info_title"
                                        ),
                                    )
                                },
                                navigationIcon = {
                                    TextButton(
                                        onClick = {
                                            if (info != null) info = null
                                            else scope.launch { drawer.open() }
                                        },
                                        modifier =
                                            Modifier.testTag(
                                                if (info == null) "open_menu" else "info_back"
                                            ),
                                    ) {
                                        Text(if (info == null) "☰" else "Back")
                                    }
                                },
                            )
                        },
                        bottomBar = {
                            if (info == null)
                                NavigationBar {
                                    for ((i, title) in
                                        listOf("My list", "Settings", "Profile").withIndex()) {
                                        NavigationBarItem(
                                            selected = tab == i,
                                            onClick = {
                                                focus.clearFocus()
                                                tab = i
                                            },
                                            icon = { Text(listOf("☑", "⚙", "○")[i]) },
                                            label = { Text(title) },
                                            modifier =
                                                Modifier.testTag(
                                                    listOf(
                                                        "nav_list",
                                                        "nav_settings",
                                                        "nav_profile",
                                                    )[i]
                                                ),
                                        )
                                    }
                                }
                        },
                    ) { padding ->
                        Column(
                            Modifier.padding(padding)
                                .padding(16.dp)
                                .verticalScroll(key(tab, info) { rememberScrollState() }),
                            verticalArrangement = Arrangement.spacedBy(12.dp),
                        ) {
                            when {
                                info != null ->
                                    Text(
                                        if (info == "Help")
                                            "Add items with the Add item button. Use Settings to choose your preferences."
                                        else "Everyday is a local demo app with sample data."
                                    )
                                tab == 0 -> {
                                    Row(
                                        Modifier.fillMaxWidth(),
                                        horizontalArrangement = Arrangement.SpaceBetween,
                                    ) {
                                        Text("${items.size} items", Modifier.testTag("item_count"))
                                        Button(
                                            onClick = {
                                                newItem = ""
                                                adding = true
                                            },
                                            modifier = Modifier.testTag("add_item"),
                                        ) {
                                            Text("Add item")
                                        }
                                    }
                                    for ((i, item) in items.withIndex()) {
                                        Check("item_${item.id}", item.title, item.done) {
                                            items[i] = item.copy(done = it)
                                        }
                                    }
                                }
                                tab == 1 -> {
                                    Text("Preferences", style = MaterialTheme.typography.titleLarge)
                                    for ((i, title) in
                                        listOf("Notifications", "Email reminders", "Sound effects")
                                            .withIndex()) {
                                        Row(
                                            Modifier.fillMaxWidth(),
                                            horizontalArrangement = Arrangement.SpaceBetween,
                                        ) {
                                            Text(title, Modifier.padding(top = 12.dp))
                                            Switch(
                                                switches[i],
                                                { switches[i] = it },
                                                Modifier.testTag("toggle_$i"),
                                            )
                                        }
                                    }
                                    HorizontalDivider()
                                    Text("Topics", style = MaterialTheme.typography.titleLarge)
                                    for ((i, title) in
                                        listOf(
                                                "Product updates",
                                                "Tips and ideas",
                                                "Community news",
                                            )
                                            .withIndex()) {
                                        Check("topic_$i", title, topics[i]) { topics[i] = it }
                                    }
                                    HorizontalDivider()
                                    Text(
                                        "Reminder frequency",
                                        style = MaterialTheme.typography.titleLarge,
                                    )
                                    for (value in listOf("Daily", "Weekly", "Never")) {
                                        Row(
                                            Modifier.fillMaxWidth().clickable { frequency = value }
                                        ) {
                                            RadioButton(
                                                selected = frequency == value,
                                                onClick = { frequency = value },
                                                modifier =
                                                    Modifier.testTag(
                                                        "frequency_${value.lowercase()}"
                                                    ),
                                            )
                                            Text(value, Modifier.padding(top = 12.dp))
                                        }
                                    }
                                    Field("settings_note", "Personal note", note, { note = it })
                                    Button(
                                        onClick = {
                                            focus.clearFocus()
                                            settingsStatus = "Preferences saved"
                                        },
                                        modifier = Modifier.testTag("save_settings"),
                                    ) {
                                        Text("Save preferences")
                                    }
                                    Text(settingsStatus, Modifier.testTag("settings_status"))
                                }
                                else -> {
                                    Text(
                                        "Your details",
                                        style = MaterialTheme.typography.titleLarge,
                                    )
                                    Text(email.ifEmpty { "Demo account" })
                                    Field("profile_name", "Name", name, { name = it })
                                    Field("profile_city", "City", city, { city = it })
                                    Check("profile_terms", "I agree to the demo terms", terms) {
                                        terms = it
                                    }
                                    Button(
                                        onClick = {
                                            focus.clearFocus()
                                            profileStatus = "Profile saved"
                                        },
                                        modifier = Modifier.testTag("save_profile"),
                                    ) {
                                        Text("Save profile")
                                    }
                                    Text(profileStatus, Modifier.testTag("profile_status"))
                                }
                            }
                        }
                    }
                }
                if (adding)
                    AlertDialog(
                        modifier = Modifier.semantics { testTagsAsResourceId = true },
                        onDismissRequest = { adding = false },
                        title = { Text("Add item", Modifier.testTag("add_dialog_title")) },
                        text = { Field("new_item_text", "Item name", newItem, { newItem = it }) },
                        confirmButton = {
                            TextButton(
                                onClick = {
                                    if (newItem.trim().isNotEmpty())
                                        items.add(Item(nextId++, newItem.trim()))
                                    focus.clearFocus()
                                    adding = false
                                },
                                modifier = Modifier.testTag("confirm_item"),
                            ) {
                                Text("OK")
                            }
                        },
                        dismissButton = {
                            TextButton(onClick = { adding = false }) { Text("Cancel") }
                        },
                    )
            }
        }
    }
}
