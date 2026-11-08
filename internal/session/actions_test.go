package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

// do runs commands, prints a line per command, then what changed; passing commands are recorded.
func TestDoRunsAndRecords(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Sign in\n- inputText: hello\n"}, t.TempDir())
	if !resp.OK || resp.Exit != 0 {
		t.Fatalf("resp %+v", resp)
	}
	lines := strings.Split(resp.Text, "\n")
	if !strings.HasPrefix(lines[0], `ok Tap on "Sign in"  `) || !strings.HasPrefix(lines[1], `ok Input text hello  `) {
		t.Errorf("text:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "screen now:\nandroid 400x800 com.app, 3 elements\n") {
		t.Errorf("first do should show the whole screen:\n%s", resp.Text)
	}
	if !reflect.DeepEqual(d.steps, []string{"- tapOn: Sign in", "- inputText: hello"}) {
		t.Errorf("steps %q", d.steps)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 325} {
		t.Errorf("taps %v", f.taps)
	}
	if !reflect.DeepEqual(f.typed, []string{"hello"}) {
		t.Errorf("typed %v", f.typed)
	}
	if d.lastDoEnd == nil || d.last != d.lastDoEnd {
		t.Error("the screen at the end of do should be remembered")
	}
}

// After the first do, do prints only what changed.
func TestDoPrintsDiff(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(label("Count 0", 0), button("Add", 100))
	addDev(s, f, "")
	call(t, s.look, nil, "")
	f.show(label("Count 0", 0), button("Add", 100), label("Added!", 200))
	resp, _ := call(t, s.do, map[string]any{"commands": "tapOn: Add"}, "")
	if !strings.HasSuffix(resp.Text, "+ @3 Text \"Added!\"\n") {
		t.Errorf("text:\n%s", resp.Text)
	}
	resp, _ = call(t, s.do, map[string]any{"commands": "inputText: x"}, "")
	if !strings.HasSuffix(resp.Text, "screen unchanged\n") {
		t.Errorf("text:\n%s", resp.Text)
	}
}

// A failing command is marked FAILED with its error and similar elements, exits 1, and is
// not recorded; the commands before it are.
func TestDoFailure(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Sign in", 100), label("Forgot password?", 200))
	d := addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- inputText: a\n- tapOn: Password reset\n- inputText: never\n", "json": true}, "")
	if resp.OK || resp.Exit != 1 {
		t.Fatalf("resp %+v", resp)
	}
	if !strings.Contains(resp.Text, "\nFAILED Tap on \"Password reset\"  ") {
		t.Errorf("text:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, `  similar: @2 Text "Forgot password?"`) {
		t.Errorf("no similar elements:\n%s", resp.Text)
	}
	if !reflect.DeepEqual(d.steps, []string{"- inputText: a"}) {
		t.Errorf("steps %q", d.steps)
	}
	if !reflect.DeepEqual(f.typed, []string{"a"}) {
		t.Errorf("typed %v: commands after the failure ran", f.typed)
	}
	var result actionResult
	if err := json.Unmarshal(resp.JSON, &result); err != nil {
		t.Fatal(err)
	}
	if result.Exit != 1 || result.Error == "" || len(result.Steps) != 2 ||
		result.Steps[0].Status != "passed" || result.Steps[1].Status != "failed" ||
		result.Screen == nil || len(result.Similar) != 1 || result.Similar[0].Label != "Forgot password?" {
		t.Errorf("structured result: %s", resp.JSON)
	}
}

// Optional (warned) and skipped commands are marked and still recorded.
func TestDoWarnedAndSkipped(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	d := addDev(s, f, "")
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "x.js"), []byte("output.ran = true"), 0o644)
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: {text: Nope, optional: true}\n- runScript:\n    file: x.js\n    when: {true: \"${false}\"}\n"}, cwd)
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	if !strings.HasPrefix(resp.Text, "warned ") || !strings.Contains(resp.Text, "\nskipped ") {
		t.Errorf("text:\n%s", resp.Text)
	}
	if len(d.steps) != 2 {
		t.Errorf("steps %q", d.steps)
	}
}

// @refs from the last look become selectors, both when run and when recorded.
func TestDoResolvesRefs(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	call(t, s.look, nil, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: @3\n- inputText: \"@3 stays in quotes\"\n"}, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	if !reflect.DeepEqual(d.steps, []string{`- tapOn: "Sign in"`, `- inputText: "@3 stays in quotes"`}) {
		t.Errorf("steps %q", d.steps)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 325} {
		t.Errorf("taps %v", f.taps)
	}
}

// A ref with no listed screen, or not on it, is refused with exit 2 before anything runs.
func TestDoBadRefs(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("OK", 0))
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "tapOn: @1"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, "no screen listed yet") {
		t.Errorf("no look: %+v", resp)
	}
	call(t, s.look, nil, "")
	resp, _ = call(t, s.do, map[string]any{"commands": "tapOn: @9"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, "@9 is not on the last listed screen") {
		t.Errorf("unknown ref: %+v", resp)
	}
	if len(f.taps) != 0 {
		t.Error("a command ran")
	}
}

// Command YAML that does not parse is refused with exit 2.
func TestDoParseError(t *testing.T) {
	s := New()
	addDev(s, newFake("e", "E", device.Android), "")
	for _, cmds := range []string{"- notACommand: x", "- tapOn: [unclosed", "- tapOn"} {
		if resp, _ := call(t, s.do, map[string]any{"commands": cmds}, ""); resp.Exit != 2 {
			t.Errorf("%q: %+v", cmds, resp)
		}
	}
	if resp, _ := call(t, s.do, map[string]any{"device": "nobody"}, ""); resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
}

// The app for launchApp & co is --app (remembered), else the open app, else the foreground app.
func TestDoAppID(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.fg = "com.foreground"
	d := addDev(s, f, "")
	call(t, s.do, map[string]any{"commands": "launchApp"}, "")
	call(t, s.do, map[string]any{"commands": "launchApp", "app": "com.given"}, "")
	call(t, s.do, map[string]any{"commands": "launchApp"}, "")
	if !reflect.DeepEqual(f.launched, []string{"com.foreground", "com.given", "com.given"}) {
		t.Errorf("launched %v", f.launched)
	}
	if d.AppID != "com.given" {
		t.Errorf("AppID %q", d.AppID)
	}
	// no app known and no foreground: launchApp fails, and do says so
	f2 := newFake("e2", "E2", device.Android)
	f2.fgErr = errBoom
	s2 := New()
	addDev(s2, f2, "")
	resp, _ := call(t, s2.do, map[string]any{"commands": "launchApp"}, "")
	if resp.Exit != 1 || !strings.Contains(resp.Text, "no appId") {
		t.Errorf("resp %+v", resp)
	}
}

// -e values reach the commands.
func TestDoEnv(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "inputText: ${WHO}-${N}", "env": map[string]string{"WHO": "bob", "N": "2"}}, "")
	if !resp.OK || !reflect.DeepEqual(f.typed, []string{"bob-2"}) {
		t.Errorf("resp %+v typed %v", resp, f.typed)
	}
}

// do still reports results when the screen cannot be read afterwards (no diff).
func TestDoScreenUnreadable(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.screenErr = errBoom
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "inputText: x"}, "")
	if !resp.OK || strings.Contains(resp.Text, "screen") {
		t.Errorf("resp %+v", resp)
	}
}

// Commands for one device run one `do` at a time.
func TestDoSerializedPerDevice(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	var mu sync.Mutex
	cur, peak := 0, 0
	f.onType = func(string) {
		mu.Lock()
		cur++
		peak = max(peak, cur)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		cur--
		mu.Unlock()
	}
	addDev(s, f, "")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call(t, s.do, map[string]any{"commands": "inputText: x"}, "")
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Errorf("%d commands ran at once on one device", peak)
	}
}

// splitItems cuts command YAML into one snippet per top-level item; a single command
// without a dash becomes one item.
func TestSplitItems(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"- tapOn: A\n- back\n", []string{"- tapOn: A", "- back"}},
		{"- tapOn:\n    id: x\n    index: 1\n- back", []string{"- tapOn:\n    id: x\n    index: 1", "- back"}},
		{"tapOn: A", []string{"- tapOn: A"}},
		{"tapOn:\n  id: x\n", []string{"- tapOn:\n    id: x"}},
		{"back", []string{"- back"}},
		{"-\n  tapOn: A\n- back", []string{"-\n  tapOn: A", "- back"}},
	}
	for _, c := range cases {
		got, err := splitItems(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitItems(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// An indented command list (valid YAML that `tb do -` passes through) is recorded item by item.
func TestSplitItemsIndentedList(t *testing.T) {
	got, _ := splitItems("  - tapOn: A\n  - back\n")
	if len(got) != 2 {
		t.Errorf("splitItems = %q, want 2 items", got)
	}
}

// Text before the first list item (a comment, a blank line) does not shift which YAML is
// recorded for which command.
func TestDoRecordsWithLeadingComment(t *testing.T) {
	s := New()
	addDev(s, newFake("e", "E", device.Android), "")
	call(t, s.do, map[string]any{"commands": "# sign in\n- inputText: a\n- inputText: b\n"}, "")
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "- inputText: a\n- inputText: b\n" {
		t.Errorf("log %q", resp.Text)
	}
}

// fmtDur prints milliseconds under a second, else tenths of seconds.
func TestFmtDur(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0ms", 12 * time.Millisecond: "12ms", 999 * time.Millisecond: "999ms", time.Second: "1.0s", 2345 * time.Millisecond: "2.3s"} {
		if got := fmtDur(d); got != want {
			t.Errorf("fmtDur(%s) = %s, want %s", d, got, want)
		}
	}
}

// fail builds an exit response with a formatted message.
func TestFail(t *testing.T) {
	r := fail(3, "x=%d %s", 7, "y")
	if r.OK || r.Exit != 3 || r.Text != "x=7 y" {
		t.Errorf("fail = %+v", r)
	}
}
