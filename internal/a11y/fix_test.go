package a11y

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixDir checks dir, applies the fixes and returns the report.
func fixDir(t *testing.T, dir string, dryRun bool) *FixReport {
	t.Helper()
	res, err := Check([]string{dir}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Fix(res, dryRun)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// added lists "line added" for each fix, in report order.
func added(rep *FixReport) []string {
	var out []string
	for _, f := range rep.Fixed {
		out = append(out, fmt.Sprintf("%d %s", f.Line, f.Added))
	}
	return out
}

func expectAdded(t *testing.T, rep *FixReport, want ...string) {
	t.Helper()
	if got := added(rep); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fixed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Android layouts get an android:id named after the click handler, the drawable or the
// view kind, unique in the file.
func TestFixAndroidLayout(t *testing.T) {
	dir := write(t, map[string]string{"app/res/layout/home.xml": `<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android">
    <ImageButton android:src="@drawable/ic_close" />
    <ImageButton android:onClick="openSettings" android:src="@drawable/gear" />
    <ImageButton
        android:src="@drawable/btn_close" />
    <Button android:layout_width="wrap_content" />
    <EditText android:inputType="text" />
    <ImageView android:clickable="true" />
    <ImageView android:clickable="true" />
    <TextView android:id="@+id/close_button" android:text="x" />
</LinearLayout>`})
	path := filepath.Join(dir, "app/res/layout/home.xml")
	rep := fixDir(t, dir, false)
	// close_button is taken (line 11), so the first close is close_button_2.
	expectAdded(t, rep,
		`3 android:id="@+id/close_button_2"`,
		`4 android:id="@+id/open_settings_button"`,
		`5 android:id="@+id/close_button_3"`,
		`7 android:id="@+id/button"`,
		`8 android:id="@+id/edit_text_field"`,
		`9 android:id="@+id/image_view_button"`,
		`10 android:id="@+id/image_view_button_2"`)
	got := read(t, path)
	for _, want := range []string{
		`<ImageButton android:id="@+id/close_button_2" android:src="@drawable/ic_close" />`,
		`<ImageButton android:id="@+id/close_button_3"` + "\n" + `        android:src="@drawable/btn_close" />`,
		`<EditText android:id="@+id/edit_text_field" android:inputType="text" />`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fixed layout lacks %q:\n%s", want, got)
		}
	}
	expect(t, rulesAt(t, dir))
	if len(rep.Left) != 0 {
		t.Errorf("left %+v", rep.Left)
	}
}

// React Native touchables get a testID from the handler, else an icon inside, else the kind.
func TestFixReactNative(t *testing.T) {
	dir := write(t, map[string]string{"src/Bar.tsx": `import { View, Pressable, TouchableOpacity } from 'react-native';
export function Bar() {
  return (
    <View>
      <Pressable onPress={handleClose}><Icon name="x" /></Pressable>
      <Pressable onPress={() => onDelete(id)}><Icon name="x" /></Pressable>
      <Pressable onPress={() => {}}><Ionicons name="share-social" /></Pressable>
      <TouchableOpacity onPress={() => {}}><MenuIcon /></TouchableOpacity>
      <Pressable onPress={() => navigation.goBack()}><Icon /></Pressable>
      <Pressable onPress={() => {}}><View /></Pressable>
      <Pressable testID="ok" onPress={go}><Icon /></Pressable>
    </View>
  );
}`})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep,
		`5 testID="close-button"`,
		`6 testID="delete-button"`,
		`7 testID="share-social-button"`,
		`8 testID="menu-button"`,
		`9 testID="go-back-button"`,
		`10 testID="pressable-button"`)
	got := read(t, filepath.Join(dir, "src/Bar.tsx"))
	if !strings.Contains(got, `<Pressable testID="close-button" onPress={handleClose}>`) {
		t.Errorf("fixed source:\n%s", got)
	}
	expect(t, rulesAt(t, dir))
}

// Web elements get a data-testid: from the handler, the link's path, the input's type.
func TestFixWeb(t *testing.T) {
	dir := write(t, map[string]string{
		"site/index.html": `<!doctype html>
<html><body>
<button onclick="closeDialog()"><svg></svg></button>
<a href="/settings/"><img src="gear.png"></a>
<input type="email">
<div role="tab"><i class="icon"></i></div>
<button><svg></svg></button>
<button><svg></svg></button>
<p data-testid="button">taken</p>
</body></html>`,
		"web/Nav.tsx": `export const Nav = () => (
  <nav>
    <button onClick={close}><CloseIcon /></button>
    <button onClick={() => setOpen(true)}><MenuIcon /></button>
  </nav>
);`,
		"web/Bar.vue": `<template>
  <button @click="openMenu"><Icon /></button>
</template>`,
	})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep,
		`3 data-testid="close-dialog-button"`,
		`4 data-testid="settings-link"`,
		`5 data-testid="email-field"`,
		`6 data-testid="tab"`,
		`7 data-testid="button-2"`,
		`8 data-testid="button-3"`,
		`2 data-testid="open-menu-button"`,
		`3 data-testid="close-button"`,
		`4 data-testid="set-open-button"`)
	if got := read(t, filepath.Join(dir, "site/index.html")); !strings.Contains(got, `<a data-testid="settings-link" href="/settings/">`) {
		t.Errorf("fixed page:\n%s", got)
	}
	expect(t, rulesAt(t, dir))
}

const composeExposed = `setContent { Box(Modifier.semantics { testTagsAsResourceId = true }) { App() } }`

// Compose: a testTag on the modifier (a new modifier argument when there is none), and the
// testTag import, when the project exposes test tags to tb.
func TestFixCompose(t *testing.T) {
	dir := write(t, map[string]string{"app/Main.kt": composeExposed, "app/Screen.kt": `package app

import androidx.compose.material3.*
import androidx.compose.ui.Modifier

@Composable fun S() {
    IconButton(onClick = { onClose() }) { Icon(Icons.Default.Close, contentDescription = null) }
    IconButton(onClick = {}, modifier = Modifier.size(48.dp)) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) }
    Image(painterResource(R.drawable.ic_logo), null, Modifier.clickable { })
    FloatingActionButton(onClick = onAdd) { Icon(Icons.Default.Add, null) }
}`})
	path := filepath.Join(dir, "app/Screen.kt")
	rep := fixDir(t, dir, false)
	// The import goes after the last one, so the elements move down a line.
	expectAdded(t, rep,
		`8 modifier = Modifier.testTag("close-button")`,
		`9 .testTag("arrow-back-button")`,
		`10 .testTag("logo-button")`,
		`11 modifier = Modifier.testTag("add-button")`)
	got := read(t, path)
	for _, want := range []string{
		"import androidx.compose.ui.Modifier\nimport androidx.compose.ui.platform.testTag\n\n@Composable",
		`IconButton(onClick = { onClose() }, modifier = Modifier.testTag("close-button")) {`,
		`modifier = Modifier.size(48.dp).testTag("arrow-back-button")) {`,
		`Image(painterResource(R.drawable.ic_logo), null, Modifier.clickable { }.testTag("logo-button"))`,
		`FloatingActionButton(onClick = onAdd, modifier = Modifier.testTag("add-button")) {`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fixed source lacks %q:\n%s", want, got)
		}
	}
	expect(t, rulesAt(t, dir))

	// Already imported (directly or with *): no second import.
	dir = write(t, map[string]string{"app/Main.kt": composeExposed, "app/Screen.kt": `import androidx.compose.ui.platform.*
@Composable fun S() { IconButton(onClick = {}) { Icon(Icons.Default.Close, null) } }`})
	fixDir(t, dir, false)
	if got := read(t, filepath.Join(dir, "app/Screen.kt")); strings.Count(got, "import") != 1 ||
		!strings.Contains(got, `IconButton(onClick = {}, modifier = Modifier.testTag("close-button"))`) {
		t.Errorf("fixed source:\n%s", got)
	}
}

// Without testTagsAsResourceId a testTag is invisible to tb: compose findings are left, with why.
func TestFixComposeNeedsExposedTags(t *testing.T) {
	src := `import androidx.compose.material3.*
@Composable fun S() { IconButton(onClick = {}) { Icon(Icons.Default.Close, null) } }`
	dir := write(t, map[string]string{"Screen.kt": src})
	rep := fixDir(t, dir, false)
	if len(rep.Fixed) != 0 || len(rep.Left) != 1 || rep.Left[0].Rule != "compose-no-label" ||
		!strings.Contains(rep.Left[0].Reason, "testTagsAsResourceId") {
		t.Errorf("report %+v", rep)
	}
	if read(t, filepath.Join(dir, "Screen.kt")) != src {
		t.Error("the file changed")
	}
}

// Flutter labels people see, and warnings, are left for a person; files stay as they were.
func TestFixLeavesWhatNeedsAPerson(t *testing.T) {
	dart := `Widget b() => IconButton(icon: Icon(Icons.close), onPressed: close);`
	swift := `Image("a").accessibilityHidden(true)`
	dir := write(t, map[string]string{"lib/a.dart": dart, "S.swift": swift})
	rep := fixDir(t, dir, false)
	if len(rep.Fixed) != 0 || len(rep.Left) != 2 {
		t.Fatalf("report %+v", rep)
	}
	for _, l := range rep.Left {
		if l.Reason == "" || l.File == "" || l.Line != 1 {
			t.Errorf("left %+v", l)
		}
	}
	if read(t, filepath.Join(dir, "lib/a.dart")) != dart || read(t, filepath.Join(dir, "S.swift")) != swift {
		t.Error("a file changed")
	}
}

// A dry run reports the same fixes and writes nothing.
func TestFixDryRun(t *testing.T) {
	page := "<button><svg></svg></button>\n"
	dir := write(t, map[string]string{"a.html": page})
	rep := fixDir(t, dir, true)
	expectAdded(t, rep, `1 data-testid="button"`)
	if read(t, filepath.Join(dir, "a.html")) != page {
		t.Error("a dry run changed the file")
	}
}

// Ignored findings, and those --changed filtered out, are not fixed.
func TestFixOnlyWhatTheCheckKept(t *testing.T) {
	page := "<!-- tb-a11y-ignore -->\n<button><svg></svg></button>\n<button><svg></svg></button>\n<button><svg></svg></button>\n"
	dir := write(t, map[string]string{"a.html": page})
	f := filepath.Join(dir, "a.html")
	res, err := CheckFiles([]string{f}, []string{dir}, Options{Lines: map[string]map[int]bool{f: {2: true, 3: true}}})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Fix(res, false)
	if err != nil {
		t.Fatal(err)
	}
	expectAdded(t, rep, `3 data-testid="button"`)
	if got := read(t, f); strings.Count(got, "data-testid") != 1 {
		t.Errorf("fixed page:\n%s", got)
	}
}

// A file that changed since the check is not touched.
func TestFixRefusesChangedFile(t *testing.T) {
	dir := write(t, map[string]string{"a.html": "<button><svg></svg></button>\n"})
	res, _ := Check([]string{dir}, Options{})
	f := filepath.Join(dir, "a.html")
	os.WriteFile(f, []byte("<p>\n<button><svg></svg></button>\n"), 0o644)
	if _, err := Fix(res, false); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Errorf("err %v", err)
	}
	if got := read(t, f); strings.Contains(got, "data-testid") {
		t.Errorf("file was edited: %s", got)
	}
}

func TestFixNames(t *testing.T) {
	for in, want := range map[string]string{
		"{handleClose}": "close", "{() => onDelete(id)}": "delete", "{close}": "close",
		"{() => navigation.goBack()}": "goBack", "{() => {}}": "", "{props.onSubmit}": "submit",
		"openSettings": "openSettings", "closeDialog()": "closeDialog", "{() => { save(); close() }}": "save",
		"{onPress}": "", "{() => setOpen(true)}": "setOpen", "{function () { go() }}": "go",
	} {
		if got := handlerName(in); got != want {
			t.Errorf("handlerName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"openSettings": "open-settings", "ic_close": "ic-close", "HTTPServer": "http-server",
		"share-social": "share-social", "  Close  Dialog ": "close-dialog", "ArrowBack": "arrow-back", "é!": "",
	} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
	if got := snake("openSettings"); got != "open_settings" {
		t.Errorf("snake = %q", got)
	}
	for in, want := range map[string]string{"@drawable/ic_close": "close", "@drawable/btn_save": "save",
		"@mipmap/logo": "logo", "R.drawable.ic_logo": "logo", "@android:drawable/ic_menu_add": "menu_add"} {
		if got := resourceName(in); got != want {
			t.Errorf("resourceName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"/settings/": "settings", "https://x.com/a/b?q=1": "b",
		"#top": "top", "/": "", "{url}": ""} {
		if got := hrefName(in); got != want {
			t.Errorf("hrefName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := withRole("close", "button", "-"); got != "close-button" {
		t.Errorf("withRole = %q", got)
	}
	if got := withRole("close-button", "button", "-"); got != "close-button" {
		t.Errorf("withRole doubled: %q", got)
	}
}

// A modifier that is not a plain chain (an if/else, an elvis, an infix then) gets the testTag
// on the whole expression, in parentheses: appended bare, it would tag only the last branch
// (issue #60).
func TestFixComposeModifierExpression(t *testing.T) {
	dir := write(t, map[string]string{"app/Main.kt": composeExposed, "app/Screen.kt": `import androidx.compose.ui.platform.testTag
@Composable fun S(big: Boolean, mod: Modifier?) {
    IconButton(onClick = onAdd, modifier = if (big) Modifier.size(48.dp) else Modifier) { Icon(Icons.Default.Add, null) }
    IconButton(onClick = onShare, modifier = mod ?: Modifier) { Icon(Icons.Default.Share, null) }
    Image(painterResource(R.drawable.ic_logo), null, Modifier.clickable { } then extra)
    IconButton(onClick = onEdit, modifier = modifier
        .padding(4.dp)) { Icon(Icons.Default.Edit, null) }
}`})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep,
		`3 .testTag("add-button")`,
		`4 .testTag("share-button")`,
		`5 .testTag("logo-button")`,
		`6 .testTag("edit-button")`)
	got := read(t, filepath.Join(dir, "app/Screen.kt"))
	for _, want := range []string{
		`modifier = (if (big) Modifier.size(48.dp) else Modifier).testTag("add-button")) {`,
		`modifier = (mod ?: Modifier).testTag("share-button")) {`,
		`null, (Modifier.clickable { } then extra).testTag("logo-button"))`,
		"modifier = modifier\n        .padding(4.dp).testTag(\"edit-button\")) {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fixed source lacks %q:\n%s", want, got)
		}
	}
	expect(t, rulesAt(t, dir))
}

// An element a loop repeats (.map, v-for, {#each}, Compose items { }, forEach
// and for) would get the same fixed id on every item, so id: would depend on which one tb
// meets first. It is left for a person, who can build a per-item id (issue #60).
func TestFixLeavesLoopItems(t *testing.T) {
	files := map[string]string{
		"app/Main.kt": composeExposed,
		"app/List.kt": `import androidx.compose.ui.platform.testTag
@Composable fun L(items: List<Item>) {
    LazyColumn {
        items(items) { item -> IconButton(onClick = { open(item) }) { Icon(Icons.Default.Info, null) } }
    }
    items.forEach { IconButton(onClick = { remove(it) }) { Icon(Icons.Default.Delete, null) } }
    for (i in items) { IconButton(onClick = { pin(i) }) { Icon(Icons.Default.Star, null) } }
    IconButton(onClick = onClose) { Icon(Icons.Default.Close, null) }
}`,
		"src/List.tsx": `import { View, Pressable } from 'react-native';
export const L = ({ items, groups }) => (
  <View>
    {items.map(i => <Pressable onPress={() => open(i)}><Icon /></Pressable>)}
    {groups.flatMap(g => g.items.map(i => (
      <View key={i.id}><Pressable onPress={() => pick(i)}><Icon /></Pressable></View>
    )))}
    <Pressable onPress={close}><Icon /></Pressable>
  </View>
);`,
		"web/List.vue": `<template>
  <ul>
    <li v-for="i in items" :key="i.id"><button @click="pick(i)"><Icon /></button></li>
  </ul>
  <button @click="clear"><Icon /></button>
</template>`,
		"web/List.svelte": `{#each items as i}
  <button on:click={() => pick(i)}><svg></svg></button>
{/each}
<button on:click={done}><svg></svg></button>`,
	}
	dir := write(t, files)
	rep := fixDir(t, dir, false)
	var fixed []string
	for _, f := range rep.Fixed {
		fixed = append(fixed, filepath.Base(f.File)+" "+f.Added)
	}
	sort.Strings(fixed)
	want := []string{
		`List.kt modifier = Modifier.testTag("close-button")`,
		`List.svelte data-testid="done-button"`,
		`List.tsx testID="close-button"`,
		`List.vue data-testid="clear-button"`,
	}
	if strings.Join(fixed, "\n") != strings.Join(want, "\n") {
		t.Errorf("fixed:\n%s\nwant:\n%s", strings.Join(fixed, "\n"), strings.Join(want, "\n"))
	}
	left := map[string]int{}
	for _, l := range rep.Left {
		if !strings.Contains(l.Reason, "per-item id") {
			t.Errorf("left without saying it needs a per-item id: %+v", l)
		}
		left[filepath.Base(l.File)]++
	}
	if left["List.kt"] != 3 || left["List.tsx"] != 2 || left["List.vue"] != 1 || left["List.svelte"] != 1 {
		t.Errorf("left %v, want 3 in List.kt, 2 in List.tsx, 1 in List.vue and 1 in List.svelte", left)
	}
	// The loops' items are as they were.
	for name, src := range files {
		got := read(t, filepath.Join(dir, name))
		for _, line := range strings.Split(src, "\n") {
			if strings.Contains(line, "items") || strings.Contains(line, "pick(i)") {
				if !strings.Contains(got, line) {
					t.Errorf("%s: a loop's line changed: %q\n%s", name, line, got)
				}
			}
		}
	}
}
