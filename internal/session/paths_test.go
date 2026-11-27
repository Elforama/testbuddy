package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
)

func writePath(t *testing.T, cwd, app, name, steps string) string {
	t.Helper()
	file := filepath.Join(cwd, "paths", app, name+".yaml")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("appId: "+app+"\n---\n"+steps), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSavePathWritesUnderTheAppID(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	d.AppID = "com.chat"
	d.steps = []string{"- launchApp", "- tapOn: \"Sign in\""}
	cwd := t.TempDir()
	resp, _ := call(t, s.save, map[string]any{"name": "sign-in"}, cwd)
	file := filepath.Join(cwd, "paths", "com.chat", "sign-in.yaml")
	if !resp.OK || !strings.HasPrefix(resp.Text, "saved path sign-in (3 commands) to "+file+"\n") {
		t.Fatalf("resp %+v", resp)
	}
	for _, want := range []string{`landmark: "Welcome back"`, "edit that line", "tb go sign-in"} {
		if !strings.Contains(resp.Text, want) {
			t.Errorf("reply lacks %q:\n%s", want, resp.Text)
		}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "appId: com.chat\n---\n- launchApp\n- tapOn: \"Sign in\"\n- assertVisible: \"Welcome back\"\n"
	if string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
	if _, err := flow.ParseFile(file); err != nil {
		t.Errorf("the path is not a flow: %v", err)
	}
	resp, _ = call(t, s.save, map[string]any{"name": "sign-in"}, cwd)
	if !resp.OK || !strings.HasPrefix(resp.Text, "replaced path sign-in (3 commands) in "+file+"\n") {
		t.Errorf("overwrite %+v", resp)
	}
}

func TestSavePathSaysWhatPreviewWarns(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	d.steps = []string{"- tapOn: {point: '20%,20%'}"}
	resp, _ := call(t, s.save, map[string]any{"name": "home"}, t.TempDir())
	if !resp.OK || !strings.Contains(resp.Text, "warning: Add a required launchApp step") ||
		!strings.Contains(resp.Text, "warning (step 1): ") {
		t.Errorf("resp %+v", resp)
	}
}

func TestSavePathWebUsesTheHost(t *testing.T) {
	s := New()
	f := newFake("web", "Chrome", device.Web)
	f.show(item{text: "Cart", class: "h1", top: 100})
	d := addDev(s, f, "")
	d.AppID = "http://localhost:7360/shop?tab=1"
	cwd := t.TempDir()
	resp, _ := call(t, s.save, map[string]any{"name": "cart"}, cwd)
	file := filepath.Join(cwd, "paths", "localhost_7360", "cart.yaml")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "url: \"http://localhost:7360/shop?tab=1\"\n---\n- assertVisible: \"Cart\"\n"; string(b) != want {
		t.Errorf("file =\n%q\nwant\n%q", b, want)
	}
}

func TestPathLandmark(t *testing.T) {
	field := item{text: "ann@example.com", class: "android.widget.EditText", top: 50}
	cases := []struct {
		name  string
		plat  device.Platform
		items []item
		want  string
	}{
		{"the first heading, over a text above it", device.Web,
			[]item{{text: "Signed in as Ann", class: "p", top: 10}, {text: "Order summary", class: "h1", top: 100}, {text: "Totals", class: "h2", top: 200}},
			`- assertVisible: "Order summary"`},
		{"else the first text that is not a control", device.Android,
			[]item{button("Back", 10), field, label("Your cart", 200), label("2 items", 300)},
			`- assertVisible: "Your cart"`},
		{"regex characters are escaped", device.Android,
			[]item{label("Total (2)", 100)}, `- assertVisible: "Total \\(2\\)"`},
		{"never what a field holds", device.Android, []item{field, button("Save", 200)}, ""},
		{"never what a web field holds", device.Web, []item{{text: "ann@example.com", class: "input:text", top: 50}}, ""},
	}
	for _, c := range cases {
		s := New()
		f := newFake("e", "E", c.plat)
		f.show(c.items...)
		d := addDev(s, f, "")
		d.AppID = "com.chat"
		if c.plat == device.Web {
			d.AppID = "https://shop.example.com/"
		}
		cwd := t.TempDir()
		resp, _ := call(t, s.save, map[string]any{"name": "p"}, cwd)
		if !resp.OK {
			t.Errorf("%s: %+v", c.name, resp)
			continue
		}
		files, _ := filepath.Glob(filepath.Join(cwd, "paths", "*", "p.yaml"))
		if len(files) != 1 {
			t.Errorf("%s: files %v", c.name, files)
			continue
		}
		b, _ := os.ReadFile(files[0])
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		last := lines[len(lines)-1]
		if c.want == "" {
			if strings.Contains(string(b), "assertVisible") || strings.Contains(string(b), "ann@example.com") ||
				!strings.Contains(resp.Text, "no landmark") {
				t.Errorf("%s: file\n%s\nreply\n%s", c.name, b, resp.Text)
			}
			continue
		}
		if last != c.want {
			t.Errorf("%s: last step %q, want %q", c.name, last, c.want)
		}
	}
}

func TestSavePathRefusesNamesThatAreNotSimple(t *testing.T) {
	s := New()
	addDev(s, newFake("e", "E", device.Android), "")
	cwd := t.TempDir()
	for _, name := range []string{"../up", "a/b", "sub\\x", ".hidden", "with space", "cart.yaml"} {
		resp, _ := call(t, s.save, map[string]any{"name": name}, cwd)
		if resp.Exit != 2 || !strings.Contains(resp.Text, "a path name is") {
			t.Errorf("%q: %+v", name, resp)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "paths")); !os.IsNotExist(err) {
		t.Errorf("a refused name wrote something: %v", err)
	}
}

func TestGoRunsThePathAndReportsArrival(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	cwd := t.TempDir()
	writePath(t, cwd, "com.app", "sign-in", "- tapOn: \"Sign in\"\n- inputText: hi\n- assertVisible: \"Welcome back\"\n")
	resp, _ := call(t, s.goPath, map[string]any{"name": "sign-in"}, cwd)
	if !resp.OK || !strings.HasPrefix(resp.Text, "arrived at \"Welcome back\"\nok Run sign-in") {
		t.Fatalf("resp %+v", resp)
	}
	if !strings.Contains(resp.Text, `Button "Sign in"`) {
		t.Errorf("no screen in the reply:\n%s", resp.Text)
	}
	if len(f.taps) != 1 || len(f.typed) != 1 || f.typed[0] != "hi" {
		t.Errorf("taps %v typed %v", f.taps, f.typed)
	}
	if log, _ := call(t, s.log, nil, ""); log.Text != "- runFlow: paths/com.app/sign-in.yaml\n" {
		t.Errorf("recorded %q", log.Text)
	}
	saved, _ := call(t, s.save, map[string]any{"path": "flows/later.yaml"}, cwd)
	b, _ := os.ReadFile(filepath.Join(cwd, "flows", "later.yaml"))
	if !saved.OK || !strings.Contains(string(b), "- runFlow: ../paths/com.app/sign-in.yaml\n") {
		t.Errorf("save %+v file\n%s", saved, b)
	}
	if _, err := flow.ParseFile(filepath.Join(cwd, "flows", "later.yaml")); err != nil {
		t.Errorf("the flow saved after tb go does not find the path: %v", err)
	}
	next, _ := call(t, s.save, map[string]any{"name": "further"}, cwd)
	b, _ = os.ReadFile(filepath.Join(cwd, "paths", "com.app", "further.yaml"))
	if !next.OK || !strings.Contains(string(b), "- runFlow: sign-in.yaml\n") {
		t.Errorf("save path %+v file\n%s", next, b)
	}
}

func TestGoFailureSaysHowToGoOn(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	cwd := t.TempDir()
	writePath(t, cwd, "com.app", "gone", "- tapOn: \"Sign in\"\n- assertVisible: \"Order summary\"\n")
	resp, _ := call(t, s.goPath, map[string]any{"name": "gone"}, cwd)
	if resp.Exit != 1 || !strings.Contains(resp.Text, "FAILED Run gone") || strings.Contains(resp.Text, "arrived") {
		t.Fatalf("resp %+v", resp)
	}
	want := "path gone: the steps before the failure ran. Go on from the screen shown; if the app changed, save the path again (tb save --path gone)\n"
	if !strings.HasSuffix(resp.Text, want) {
		t.Errorf("reply ends\n%s", resp.Text)
	}
	if len(f.taps) != 1 {
		t.Errorf("taps %v", f.taps)
	}
	if log, _ := call(t, s.log, nil, ""); log.Text != "no commands recorded yet\n" {
		t.Errorf("a failed path was recorded: %q", log.Text)
	}
}

func TestGoUnknownAndMissingNames(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	cwd := t.TempDir()
	resp, _ := call(t, s.goPath, map[string]any{}, cwd)
	if !resp.OK || resp.Text != "no paths saved for com.app here: save one with tb save --path <name>\n" {
		t.Errorf("no paths: %+v", resp)
	}
	resp, _ = call(t, s.goPath, map[string]any{"name": "checkout"}, cwd)
	if resp.Exit != 2 || resp.Text != "no path \"checkout\" for com.app: none saved here (tb save --path checkout)" {
		t.Errorf("unknown, none saved: %+v", resp)
	}
	writePath(t, cwd, "com.app", "settings", "- tapOn: Settings\n- assertVisible: {id: theme}\n")
	writePath(t, cwd, "com.app", "checkout", "- tapOn: Cart\n- assertVisible: \"Total \\\\(2\\\\)\"\n")
	writePath(t, cwd, "com.other", "elsewhere", "- assertVisible: Other\n")
	resp, _ = call(t, s.goPath, map[string]any{}, cwd)
	if want := "checkout -> \"Total (2)\"\nsettings -> #theme\n"; !resp.OK || resp.Text != want {
		t.Errorf("list = %q, want %q", resp.Text, want)
	}
	resp, _ = call(t, s.goPath, map[string]any{"name": "chekout"}, cwd)
	if resp.Exit != 2 || resp.Text != "no path \"chekout\" for com.app: have checkout, settings" {
		t.Errorf("unknown: %+v", resp)
	}
	resp, _ = call(t, s.goPath, map[string]any{"name": "../com.other/elsewhere"}, cwd)
	if resp.Exit != 2 || len(f.taps) != 0 {
		t.Errorf("a name that is a file path: %+v", resp)
	}
}

func TestOpenListsTheAppsPaths(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	cwd := t.TempDir()
	resp, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "app": "com.chat"}, cwd)
	if !resp.OK || strings.Contains(resp.Text, "paths") {
		t.Fatalf("no paths yet: %+v", resp)
	}
	writePath(t, cwd, "com.chat", "settings", "- assertVisible: Settings\n")
	writePath(t, cwd, "com.chat", "checkout", "- assertVisible: Total\n")
	writePath(t, cwd, "com.other", "elsewhere", "- assertVisible: Other\n")
	resp, _ = call(t, s.open, map[string]any{"device": "emulator-5554", "app": "com.chat"}, cwd)
	if !resp.OK || !strings.HasPrefix(resp.Text, "connected ") || !strings.HasSuffix(resp.Text, "\npaths: checkout, settings (tb go <name>)\n") {
		t.Errorf("resp %+v", resp)
	}
}
