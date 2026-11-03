package engine

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/flow"
)

// Describe words every command as the format's reports do.
func TestDescribe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub.yaml", "appId: app\n---\n- back")
	writeFile(t, dir, "s.js", "1")
	writeFile(t, dir, "a.png", "x")
	for _, tc := range []struct{ yaml, want string }{
		{"tapOn: Login", `Tap on "Login"`},
		{"tapOn: {point: '10,20'}", "Tap on point (10,20)"},
		{"tapOn: {id: x, index: 2, below: Title, enabled: true}", `Tap on id: x, index: 2, below "Title", enabled: true`},
		{"tapOn: {css: .btn, selected: false, checked: true, focused: true}", "Tap on css: .btn, selected: false, checked: true, focused: true"},
		{"tapOn: {text: A, above: {id: b}, leftOf: C, rightOf: D, containsChild: E, childOf: {id: f}}", `Tap on "A", above id: b, left of "C", right of "D", containing child "E", child of id: f`},
		{"tapOn: {optional: true}", "Tap on element"},
		{"doubleTapOn: X", `Double tap on "X"`},
		{"longPressOn: X", `Long press on "X"`},
		{"assertVisible: X", `Assert that "X" is visible`},
		{"assertNotVisible: {id: x}", "Assert that id: x is not visible"},
		{"assertTrue: ${a}", `Assert that "${a}" is true`},
		{"extendedWaitUntil: {visible: X, timeout: 1}", `Wait until "X" is visible`},
		{"extendedWaitUntil: {notVisible: {id: y}}", "Wait until id: y is not visible"},
		{"inputText: hi", "Input text hi"},
		{"inputRandomEmail", "Input random email"},
		{"inputRandomPersonName", "Input random personname"},
		{"eraseText: 3", "Erase 3 characters"},
		{"copyTextFrom: {id: a}", "Copy text from id: a"},
		{"setClipboard: t", "Set clipboard to t"},
		{"pasteText", "Paste text"},
		{"swipe: {direction: UP}", "Swipe UP"},
		{"swipe: {start: '1,2', end: '3,4'}", "Swipe from (1,2) to (3,4)"},
		{"swipe: {from: {id: c}, direction: LEFT}", "Swipe LEFT from id: c"},
		{"scroll", "Scroll vertically"},
		{"scrollUntilVisible: {element: X}", `Scroll DOWN until "X" is visible`},
		{"scrollUntilVisible: {element: X, direction: left}", `Scroll LEFT until "X" is visible`},
		{"back", "Press back"},
		{"hideKeyboard", "Hide keyboard"},
		{"pressKey: Enter", "Press Enter key"},
		{"launchApp", "Launch app "},
		{"launchApp: com.x", `Launch app "com.x"`},
		{"stopApp", "Stop app "},
		{"killApp: k", `Kill app "k"`},
		{"clearState", "Clear state of app"},
		{"clearState: c", `Clear state of "c"`},
		{"clearKeychain", "Clear keychain"},
		{"setPermissions: {permissions: {a: b}}", "Set permissions"},
		{"openLink: https://x.test", "Open https://x.test"},
		{"runFlow: sub.yaml", "Run sub"},
		{"runFlow: {file: sub.yaml, when: {platform: iOS}}", "Run sub when platform is ios"},
		{"runFlow: {commands: [back]}", "Run flow"},
		{"runFlow: {commands: [back], when: {visible: X, notVisible: Y}}", `Run flow when "X" is visible and "Y" is not visible`},
		{"runFlow: {commands: [back], when: {visible: X, label: logged in}}", "Run flow when logged in"},
		{"runScript: s.js", "Run s.js"},
		{"evalScript: ${1}", "Run ${1}"},
		{"repeat: {times: 3, commands: [back]}", "Repeat 3 times"},
		{"repeat: {while: {visible: X}, commands: [back]}", `Repeat while "X" is visible`},
		{"repeat: {times: 3, while: {true: 'a'}, commands: [back]}", `Repeat while "a" is true (up to 3 times)`},
		{"repeat: {commands: [back]}", "Repeat"},
		{"retry: {commands: [back]}", "Retry up to 1 times"},
		{"retry: {maxRetries: 2, commands: [back]}", "Retry up to 2 times"},
		{"takeScreenshot: a", "Take screenshot a"},
		{"startRecording: r", "Start recording r"},
		{"stopRecording", "Stop recording"},
		{"assertScreenshot: a", "Assert screenshot a"},
		{"waitForAnimationToEnd", "Wait for animation to end"},
		{"setLocation: {latitude: '1', longitude: '2'}", "Set location (1, 2)"},
		{"travel: {points: ['1,2', '3,4']}", "Travel through 2 points"},
		{"setOrientation: PORTRAIT", "Set orientation PORTRAIT"},
		{"addMedia: [a.png]", "Add 1 media files"},
		{"setAirplaneMode: enabled", "Set airplane mode on"},
		{"toggleAirplaneMode", "Toggle airplane mode"},
		{"setDarkMode: disabled", "Set dark mode off"},
		{"toggleDarkMode", "Toggle dark mode"},
		{"assertDarkMode", "Assert dark mode"},
		{"assertLightMode", "Assert light mode"},
		{"assertWithAI: looks ok", "assertWithAI looks ok"},
		{"signal: s", "Signal s"},
		{"waitForSignal: s", "Wait for signal s"},
	} {
		cmds, err := flow.ParseCommands(dir, []byte("- "+tc.yaml))
		if err != nil {
			t.Fatalf("%s: %v", tc.yaml, err)
		}
		if got := Describe(cmds[0]); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.yaml, got, tc.want)
		}
	}
	for _, tc := range []struct {
		c    *flow.Command
		want string
	}{
		{&flow.Command{Name: "on", On: "alice"}, "On alice"},
		{&flow.Command{Name: "parallel", ParallelOrder: []string{"alice", "bob"}}, "In parallel: alice, bob"},
		{&flow.Command{Name: "inputRandomShoeSize", RandomKind: "ShoeSize"}, "Input random shoesize"},
		{&flow.Command{Name: "defineVariables"}, "defineVariables"},
	} {
		if got := Describe(tc.c); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.c.Name, got, tc.want)
		}
	}
}

// describeCondition: the label if any, else each part joined with "and".
func TestDescribeCondition(t *testing.T) {
	yes := "${ok}"
	for _, tc := range []struct {
		c    *flow.Condition
		want string
	}{
		{nil, ""},
		{&flow.Condition{}, ""},
		{&flow.Condition{Label: "ready", Platform: "ios"}, "ready"},
		{&flow.Condition{Platform: "android", Visible: &flow.Selector{ID: "a"}, NotVisible: &flow.Selector{Text: "b"}, True: &yes},
			`platform is android and id: a is visible and "b" is not visible and "${ok}" is true`},
	} {
		if got := describeCondition(tc.c); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
	if selDesc(nil) != "element" || selDesc(&flow.Selector{}) != "element" {
		t.Error("an empty selector is an element")
	}
}

// The runner's descriptions: labels (evaluated unless raw), and the flow's appId filled in.
func TestRunnerDescribe(t *testing.T) {
	r := New(&fakeDriver{screen: static()}, quick)
	r.appID = "com.app"
	r.pushEnv()
	r.env()["W"] = "world"
	for _, tc := range []struct {
		c    *flow.Command
		want string
	}{
		{&flow.Command{Name: "tapOn", Label: "Say ${W}", Selector: &flow.Selector{Text: "x"}}, "Say world"},
		{&flow.Command{Name: "inputText", Label: "Raw ${W}", LabelRaw: true}, "Raw ${W}"},
		{&flow.Command{Name: "tapOn", Label: "Broken ${(}"}, "Broken ${(}"},
		{&flow.Command{Name: "launchApp"}, `Launch app "com.app"`},
		{&flow.Command{Name: "stopApp"}, `Stop app "com.app"`},
		{&flow.Command{Name: "killApp"}, `Kill app "com.app"`},
		{&flow.Command{Name: "clearState"}, `Clear state of "com.app"`},
		{&flow.Command{Name: "launchApp", AppID: "other"}, `Launch app "other"`},
		{&flow.Command{Name: "inputText", Text: "${W}"}, "Input text ${W}"},
	} {
		if got := r.describe(tc.c); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.c, got, tc.want)
		}
	}
}

// A finished command's reported description shows ${...} values, except for commands whose
// scripts have side effects, labelled ones, and ones that do not evaluate.
func TestEvaluatedDesc(t *testing.T) {
	rec := &recorder{}
	o := quick
	o.Reporter = rec
	src := "appId: app\nenv:\n  X: ex\n---\n" +
		"- inputText: ${X}\n" +
		"- evalScript: ${output.n = 1}\n" +
		"- assertTrue: ${X == 'ex'}\n" +
		"- runFlow: {when: {true: '${X}'}, commands: [{inputText: \"${X}\"}]}\n" +
		"- inputText: {text: \"${X}\", label: \"Typing ${X}\"}\n" +
		"- inputText: \"${(}\"\n"
	runSrc(t, &fakeDriver{screen: static()}, o, t.TempDir(), src)
	var starts, ends []string
	for _, e := range rec.all() {
		if e.Cmd == nil || e.Depth != 0 {
			continue
		}
		if e.Start {
			starts = append(starts, e.Desc)
		} else {
			ends = append(ends, e.Desc)
		}
	}
	wantStarts := []string{"Input text ${X}", "Run ${output.n = 1}", `Assert that "${X == 'ex'}" is true`, `Run flow when "${X}" is true`, "Typing ${X}", "Input text ${(}"}
	wantEnds := []string{"Input text ex", "Run ${output.n = 1}", `Assert that "${X == 'ex'}" is true`, `Run flow when "${X}" is true`, "Typing ${X}", "Input text ${(}"}
	if strings.Join(starts, "|") != strings.Join(wantStarts, "|") {
		t.Errorf("start descriptions\n%q\nwant\n%q", starts, wantStarts)
	}
	if strings.Join(ends, "|") != strings.Join(wantEnds, "|") {
		t.Errorf("end descriptions\n%q\nwant\n%q", ends, wantEnds)
	}
}

// Small helpers used by the descriptions.
func TestDescribeHelpers(t *testing.T) {
	if quoteOr("", "d") != "d" || quoteOr("x", "d") != `"x"` {
		t.Error("quoteOr")
	}
	if orDefault("", "d") != "d" || orDefault("x", "d") != "x" {
		t.Error("orDefault")
	}
	if onOff(true) != "on" || onOff(false) != "off" {
		t.Error("onOff")
	}
	if baseName("/a/b.js") != "b.js" || baseName("b.js") != "b.js" {
		t.Error("baseName")
	}
}
