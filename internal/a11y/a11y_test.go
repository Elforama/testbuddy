package a11y

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// write puts files (path -> content) in a temp folder and returns it.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, src := range files {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// rulesAt checks dir and returns "rule@line" for every finding, sorted.
func rulesAt(t *testing.T, dir string) []string {
	t.Helper()
	res, err := Check([]string{dir}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range res.Findings {
		out = append(out, fmt.Sprintf("%s@%03d", f.Rule, f.Line))
	}
	sort.Strings(out)
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestAndroidLayout(t *testing.T) {
	dir := write(t, map[string]string{"app/res/layout/home.xml": `<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android">
    <ImageButton android:src="@drawable/close" />
    <ImageButton android:id="@+id/close" android:src="@drawable/close" />
    <ImageView android:contentDescription="Logo" android:clickable="true" />
    <ImageView android:src="@drawable/deco" />
    <Button android:text="Save" />
    <Button android:layout_width="wrap_content" />
    <EditText android:hint="Email" />
    <com.google.android.material.floatingactionbutton.FloatingActionButton
        android:src="@drawable/add" />
    <TextView android:onClick="open" />
</LinearLayout>`,
		// Not a layout folder: ignored.
		"app/res/values/strings.xml": `<resources xmlns:android="http://schemas.android.com/apk/res/android"><Button/></resources>`,
	})
	expect(t, rulesAt(t, dir), "android-no-selector@003", "android-no-id@008", "android-no-selector@010", "android-no-id@012")
}

func TestComposeTestTagNeedsResourceID(t *testing.T) {
	screen := `import androidx.compose.material3.*
@Composable fun Login() {
    Button(onClick = {}, modifier = Modifier.testTag("login")) { Text("Log in") }
}`
	dir := write(t, map[string]string{"app/Login.kt": screen})
	expect(t, rulesAt(t, dir), "compose-testtag-hidden@003")
	res, _ := Check([]string{dir}, Options{})
	if el := res.Findings[0].Element; el.ID != "login" {
		t.Errorf("element %+v, want #login", el)
	}

	// Set anywhere in the project: fine, even when only Login.kt is checked.
	dir = write(t, map[string]string{"app/Login.kt": screen,
		"app/Main.kt": `setContent { Box(Modifier.semantics { testTagsAsResourceId = true }) { App() } }`})
	res, err := CheckFiles([]string{filepath.Join(dir, "app/Login.kt")}, nil, Options{Root: dir})
	if err != nil || len(res.Findings) != 0 {
		t.Errorf("findings %+v, err %v", res.Findings, err)
	}
}

func TestComposeLabelsAndHiding(t *testing.T) {
	dir := write(t, map[string]string{"Screen.kt": `import androidx.compose.ui.semantics.*
@Composable fun S() {
    IconButton(onClick = {}) { Icon(Icons.Default.Close, contentDescription = null) }
    IconButton(onClick = {}) { Icon(Icons.Default.Close, contentDescription = "Close") }
    IconButton(onClick = {}) { Icon(Icons.Default.Close, "Close") }
    IconButton(onClick = {}) { Icon(Icons.Default.Close, null) }
    FloatingActionButton(onClick = {}) { Text("New") }
    Image(painterResource(R.drawable.logo), null, Modifier.clickable { })
    Image(painterResource(R.drawable.deco), null)
    Box(Modifier.clearAndSetSemantics { }) { Text("hidden") }
    Box(Modifier.clearAndSetSemantics { contentDescription = "Card" }) { Text("ok") }
    Box(Modifier.semantics { invisibleToUser() })
    // IconButton(onClick = {}) { Icon(x, null) } in a comment
}`})
	expect(t, rulesAt(t, dir), "compose-no-label@003", "compose-no-label@006", "compose-no-label@008",
		"compose-hidden@010", "compose-hidden@012")
}

func TestSwift(t *testing.T) {
	dir := write(t, map[string]string{"App/Home.swift": `struct Home: View {
    var body: some View {
        Image("deco").accessibilityHidden(true)
        Image("deco").accessibilityHidden(false)
        HStack { Text("A"); Text("B") }
            .accessibilityElement(children: .ignore)
        HStack { Text("A") }
            .accessibilityElement(children: .ignore)
            .accessibilityIdentifier("row")
        HStack { Text("A") }.accessibilityElement(children: .combine)
    }
}
class Cell: UITableViewCell { func setup() { contentView.accessibilityElementsHidden = true } }`,
		"AppTests/HomeTests.swift": `x.accessibilityHidden(true)`, // tests are skipped
	})
	expect(t, rulesAt(t, dir), "ios-hidden@003", "ios-children-ignored@006", "ios-hidden@013")
}

func TestReactNative(t *testing.T) {
	dir := write(t, map[string]string{"src/Login.tsx": `import { View, Text, Pressable, TouchableOpacity } from 'react-native';
export function Login({ items }: { items: Array<string> }) {
  const n = items.length < 3 ? 1 : 2;
  return (
    <View>
      <Pressable onPress={go}><Icon name="close" /></Pressable>
      <Pressable testID="ok" onPress={go}><Icon name="close" /></Pressable>
      <Pressable onPress={go}><Text>Don't save</Text></Pressable>
      <TouchableOpacity testID="row" onPress={go}>
        <Text testID="row_title">{title}</Text>
      </TouchableOpacity>
      <TouchableOpacity accessible={false} onPress={go}>
        <Text testID="inner">Hi</Text>
      </TouchableOpacity>
      <View importantForAccessibility="no-hide-descendants"><Text>x</Text></View>
      <View accessibilityElementsHidden><Text>y</Text></View>
      {items.map(i => <Pressable key={i} onPress={go}>{i}</Pressable>)}
      <Pressable {...props} />
    </View>
  );
}`})
	expect(t, rulesAt(t, dir), "rn-no-selector@006", "rn-nested-testid@010", "rn-hidden@015", "rn-hidden@016")
}

func TestWeb(t *testing.T) {
	dir := write(t, map[string]string{
		"site/index.html": `<!doctype html>
<html><body>
<button><svg><path d="M0"/></svg></button>
<button aria-label="Close"><svg></svg></button>
<button>Save</button>
<a href="/"><img src="logo.png" alt="Home"></a>
<input type="email">
<input type="email" placeholder="Email">
<input type="hidden">
<div role="button" class="x"><i class="icon-x"></i></div>
<script>if (a < b) { document.body.innerHTML = '<button></button>' }</script>
</body></html>`,
		"web/Nav.tsx": `export const Nav = () => (
  <nav>
    <button onClick={close}><CloseIcon /></button>
    <button onClick={close}><Trans>Close</Trans></button>
    <a href="/home" data-testid="home"><img src={logo} /></a>
    <button>{label}</button>
  </nav>
);`,
		"web/Bar.vue": `<template>
  <button @click="x"><Icon /></button>
  <button :aria-label="t('close')"><Icon /></button>
  <button>{{ label }}</button>
</template>
<script>export default { data() { return { a: 1 < 2 } } }</script>`,
	})
	got := rulesAt(t, dir)
	expect(t, got, "web-no-selector@002", "web-no-selector@003", "web-no-selector@003",
		"web-no-selector@006", "web-no-selector@007", "web-no-selector@010")
}

func TestFlutter(t *testing.T) {
	dir := write(t, map[string]string{"lib/home.dart": `Widget build(BuildContext c) {
  return Column(children: [
    IconButton(icon: Icon(Icons.close), onPressed: close),
    IconButton(icon: Icon(Icons.close), tooltip: 'Close', onPressed: close),
    FloatingActionButton(onPressed: add, child: Icon(Icons.add)),
    FloatingActionButton.extended(onPressed: add, label: Text('Add')),
    ExcludeSemantics(child: Text('x')),
    ExcludeSemantics(excluding: false, child: Text('y')),
  ]);
}`})
	expect(t, rulesAt(t, dir), "flutter-no-label@003", "flutter-no-label@005", "flutter-hidden@007")
}

func TestIgnoreComments(t *testing.T) {
	dir := write(t, map[string]string{"S.swift": `
// tb-a11y-ignore: ios-hidden
Image("a").accessibilityHidden(true)
Image("b").accessibilityHidden(true) // tb-a11y-ignore
// tb-a11y-ignore: ios-children-ignored
Image("c").accessibilityHidden(true)
`})
	expect(t, rulesAt(t, dir), "ios-hidden@006")
}

func TestLinesFilter(t *testing.T) {
	dir := write(t, map[string]string{"S.swift": "a.accessibilityHidden(true)\nb.accessibilityHidden(true)\n"})
	f := filepath.Join(dir, "S.swift")
	res, err := CheckFiles([]string{f}, []string{dir}, Options{Lines: map[string]map[int]bool{f: {2: true}}})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Line != 2 {
		t.Errorf("findings %+v, err %v", res.Findings, err)
	}
}

func TestWantedAndWalk(t *testing.T) {
	for p, want := range map[string]bool{
		"src/App.tsx": true, "node_modules/x/a.js": false, "app/build/gen/a.kt": false,
		"src/App.test.tsx": false, ".github/x.html": false, "README.md": false, "Tests/HomeTests.swift": false,
	} {
		if Wanted(p) != want {
			t.Errorf("Wanted(%q) = %v", p, !want)
		}
	}
	dir := write(t, map[string]string{"node_modules/x/a.html": "<button></button>", "a.html": "<button>ok</button>"})
	res, _ := Check([]string{dir}, Options{})
	if res.Files != 1 || len(res.Findings) != 0 {
		t.Errorf("files %d findings %+v", res.Files, res.Findings)
	}
}

func TestElementLine(t *testing.T) {
	if got := (Element{Kind: "Pressable", Label: "Close", ID: "close"}).Line(); got != `Pressable "Close" #close` {
		t.Errorf("got %s", got)
	}
}
