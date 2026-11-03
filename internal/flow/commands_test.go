package flow

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestEveryCommandHasAnExample parses one valid form of every command key, so a new
// command without parser coverage fails here.
func TestEveryCommandHasAnExample(t *testing.T) {
	examples := map[string]struct{ src, name string }{
		"tapOn":                  {"tapOn: Login", ""},
		"doubleTapOn":            {"doubleTapOn: Login", ""},
		"longPressOn":            {"longPressOn: Login", ""},
		"assertVisible":          {"assertVisible: Hi", ""},
		"assertNotVisible":       {"assertNotVisible: Bye", ""},
		"assertTrue":             {"assertTrue: ${1 == 1}", ""},
		"extendedWaitUntil":      {"extendedWaitUntil: {visible: Hi, timeout: 1000}", ""},
		"assertScreenshot":       {"assertScreenshot: shot", ""},
		"assertWithAI":           {"assertWithAI: the cart is empty", ""},
		"assertNoDefectsWithAI":  {"assertNoDefectsWithAI", ""},
		"extractTextWithAI":      {"extractTextWithAI: the price", ""},
		"inputText":              {"inputText: hello", ""},
		"inputRandomText":        {"inputRandomText", ""},
		"inputRandomNumber":      {"inputRandomNumber", ""},
		"inputRandomEmail":       {"inputRandomEmail", ""},
		"inputRandomPersonName":  {"inputRandomPersonName", ""},
		"inputRandomCityName":    {"inputRandomCityName", ""},
		"inputRandomCountryName": {"inputRandomCountryName", ""},
		"inputRandomColorName":   {"inputRandomColorName", ""},
		"eraseText":              {"eraseText", ""},
		"pasteText":              {"pasteText", ""},
		"copyTextFrom":           {"copyTextFrom: {id: code}", ""},
		"setClipboard":           {"setClipboard: abc", ""},
		"swipe":                  {"swipe: {direction: UP}", ""},
		"scroll":                 {"scroll", ""},
		"scrollUntilVisible":     {"scrollUntilVisible: {element: Footer}", ""},
		"back":                   {"back", ""},
		"hideKeyboard":           {"hideKeyboard", ""},
		"pressKey":               {"pressKey: Enter", ""},
		"launchApp":              {"launchApp", ""},
		"stopApp":                {"stopApp", ""},
		"killApp":                {"killApp", ""},
		"clearState":             {"clearState", ""},
		"clearKeychain":          {"clearKeychain", ""},
		"setPermissions":         {"setPermissions: {permissions: {all: allow}}", ""},
		"openLink":               {"openLink: https://example.com", ""},
		"runFlow":                {"runFlow: sub.yaml", ""},
		"runScript":              {"runScript: s.js", ""},
		"evalScript":             {"evalScript: ${output.x = 1}", ""},
		"repeat":                 {"repeat: {times: 2, commands: [back]}", ""},
		"retry":                  {"retry: {maxRetries: 2, commands: [back]}", ""},
		"takeScreenshot":         {"takeScreenshot: shot", ""},
		"startRecording":         {"startRecording: rec", ""},
		"stopRecording":          {"stopRecording", ""},
		"waitForAnimationToEnd":  {"waitForAnimationToEnd", ""},
		"setLocation":            {"setLocation: {latitude: 1, longitude: 2}", ""},
		"travel":                 {`travel: {points: ["1,2", "3,4"]}`, ""},
		"setOrientation":         {"setOrientation: PORTRAIT", ""},
		"addMedia":               {"addMedia: [pic.png]", ""},
		"setAirplaneMode":        {"setAirplaneMode: enabled", ""},
		"toggleAirplaneMode":     {"toggleAirplaneMode", ""},
		"setDarkMode":            {"setDarkMode: disabled", ""},
		"toggleDarkMode":         {"toggleDarkMode", ""},
		"assertDarkMode":         {"assertDarkMode", ""},
		"assertLightMode":        {"assertLightMode", ""},
		"action":                 {"action: back", "back"},
		"signal":                 {"signal: ready", ""},
		"waitForSignal":          {"waitForSignal: ready", ""},
		"parallel":               {"", ""}, // multi-device only: see multidevice_test.go
	}
	var missing []string
	for k := range commandKeys {
		if _, ok := examples[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("commands without an example: %v", missing)
	}
	dir := writeFiles(t, map[string]string{"sub.yaml": hdr + "- back\n", "s.js": "output.x = 1", "pic.png": "png"})
	for key, ex := range examples {
		if ex.src == "" {
			continue
		}
		t.Run(key, func(t *testing.T) {
			f, err := ParseBytes(filepath.Join(dir, "main.yaml"), []byte(hdr+"- "+ex.src+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			want := key
			if ex.name != "" {
				want = ex.name
			}
			if len(f.Commands) != 1 || f.Commands[0].Name != want || f.Commands[0].Line != 3 {
				t.Fatalf("got %v", names(f.Commands))
			}
		})
	}
}

// TestBareCommands pins every command usable as a plain string list item.
func TestBareCommands(t *testing.T) {
	for name := range bareCommands {
		t.Run(name, func(t *testing.T) {
			c := step(t, "- "+name+"\n")
			want := name
			if name == "hide keyboard" {
				want = "hideKeyboard" // Maestro's spelled-out alias
			}
			if c.Name != want || c.Line != 3 {
				t.Errorf("Name %q Line %d", c.Name, c.Line)
			}
		})
	}
}

// TestCommandsNeedingAValue pins that a command needing options, written bare, says so.
func TestCommandsNeedingAValue(t *testing.T) {
	for name := range commandKeys {
		if bareCommands[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			e := parseErr(t, hdr+"- "+name+"\n")
			want := fmt.Sprintf("Missing Command Options: %s needs a value", name)
			if !strings.Contains(e.Msg, want) || e.Line != 3 {
				t.Errorf("%v", e)
			}
		})
	}
}

// TestCommandsWithNullValue pins that `- cmd:` with nothing after it is rejected.
func TestCommandsWithNullValue(t *testing.T) {
	for _, name := range []string{"tapOn", "back", "launchApp", "inputText", "runFlow", "swipe"} {
		e := parseErr(t, hdr+"- "+name+":\n")
		if !strings.Contains(e.Msg, "Incorrect Command Format: "+name+" requires additional options") {
			t.Errorf("%s: %v", name, e)
		}
	}
}

// TestItemErrors pins rejections of malformed list items.
func TestItemErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"unknown bare command", "- back\n- frobnicate\n", `Invalid Command: "frobnicate"`, 4},
		{"unknown map command", "- frobnicate: X\n", `Invalid Command: "frobnicate"`, 3},
		{"numeric item", "- 42\n", `Invalid Command: "42"`, 3},
		{"list item", "- [back]\n", "each command is a string or a single-key map", 3},
		{"empty map", "- {}\n", "Invalid command format: empty map", 3},
		{"two keys", "- tapOn: X\n  optional: true\n", `unexpected top-level field "optional" next to "tapOn"`, 4},
		{"typo gets a suggestion", "- tapOnn: X\n", `Invalid Command: "tapOnn". Did you mean tapOn?`, 3},
		{"bare typo gets a suggestion", "- hideKeybord\n", "Did you mean hideKeyboard?", 3},
		{"case typo gets a suggestion", "- LAUNCHAPP\n", "Did you mean launchApp?", 3},
	})
	e := parseErr(t, hdr+"- zzzzzzzzzzzz: X\n")
	if strings.Contains(e.Msg, "Did you mean") {
		t.Errorf("far-off names get no suggestion: %v", e)
	}
}

// ---- taps ----

// TestTapOnScalar pins the defaults of the short tapOn form.
func TestTapOnScalar(t *testing.T) {
	c := step(t, "- tapOn: Login\n")
	if c.Selector == nil || c.Selector.Text != "Login" {
		t.Fatalf("selector: %+v", c.Selector)
	}
	if c.Repeat != 0 || c.LongPress || c.Optional || c.Point != "" || c.Label != "" || c.RetryTapIfNoChange || c.WaitUntilVisible {
		t.Errorf("defaults: %+v", c)
	}
	if !c.Mutating() {
		t.Error("taps are device interactions")
	}
}

// TestTapOnMapOptions pins every tap option of the map form.
func TestTapOnMapOptions(t *testing.T) {
	c := step(t, `- tapOn:
    text: Login
    label: Tap the login button
    optional: true
    repeat: 3
    delay: 250
    retryTapIfNoChange: true
    waitUntilVisible: true
    waitToSettleTimeoutMs: 1500
`)
	if c.Selector.Text != "Login" || c.Label != "Tap the login button" || !c.Optional {
		t.Errorf("selector/label/optional: %+v", c)
	}
	if c.Repeat != 3 || c.Delay != 250 || !c.RetryTapIfNoChange || !c.WaitUntilVisible || c.WaitToSettleTimeoutMs != 1500 {
		t.Errorf("options: %+v", c)
	}
}

// TestTapOnMapDefaults pins defaults and clamps of the map form.
func TestTapOnMapDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		delay, settle int
	}{
		{"defaults", "- tapOn: {id: x}\n", 100, 0},
		{"negative delay resets", "- tapOn: {id: x, delay: -5}\n", 100, 0},
		{"zero delay kept", "- tapOn: {id: x, delay: 0}\n", 0, 0},
		{"settle capped at 30s", "- tapOn: {id: x, waitToSettleTimeoutMs: 60000}\n", 100, 30000},
		{"settle under cap", "- tapOn: {id: x, waitToSettleTimeoutMs: 29999}\n", 100, 29999},
		{"float delay truncates", "- tapOn: {id: x, delay: 1.9}\n", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := step(t, tc.body)
			if c.Delay != tc.delay || c.WaitToSettleTimeoutMs != tc.settle {
				t.Errorf("delay %d settle %d", c.Delay, c.WaitToSettleTimeoutMs)
			}
		})
	}
}

// TestTapVariants pins how doubleTapOn and longPressOn set Repeat/LongPress.
func TestTapVariants(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		repeat     int
		long       bool
	}{
		{"tapOn repeat", "- tapOn: {text: X, repeat: 4}\n", 4, false},
		{"doubleTapOn scalar", "- doubleTapOn: X\n", 2, false},
		{"doubleTapOn always taps twice", "- doubleTapOn: {text: X, repeat: 5}\n", 2, false},
		{"longPressOn scalar", "- longPressOn: X\n", 0, true},
		{"longPressOn ignores repeat", "- longPressOn: {text: X, repeat: 3}\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := step(t, tc.body)
			if c.Repeat != tc.repeat || c.LongPress != tc.long || c.Selector.Text != "X" {
				t.Errorf("repeat %d long %v", c.Repeat, c.LongPress)
			}
		})
	}
}

// TestTapOnPoint pins point taps: alone they drop the selector, with one they stay relative.
func TestTapOnPoint(t *testing.T) {
	c := step(t, "- tapOn: {point: \"50%,90%\"}\n")
	if c.Selector != nil || c.Point != "50%,90%" {
		t.Errorf("screen point: %+v %q", c.Selector, c.Point)
	}
	c = step(t, "- tapOn: {point: \"540,1800\", label: px}\n")
	if c.Selector != nil || c.Point != "540,1800" || c.Label != "px" {
		t.Errorf("pixel point: %+v", c)
	}
	c = step(t, "- tapOn: {id: card, point: \"10%,10%\"}\n")
	if c.Selector == nil || c.Selector.ID != "card" || c.Point != "10%,10%" {
		t.Errorf("element-relative point: %+v %q", c.Selector, c.Point)
	}
	c = step(t, "- longPressOn: {point: \"1,2\"}\n")
	if c.Selector != nil || !c.LongPress {
		t.Errorf("long press point: %+v", c)
	}
}

// TestTapOnOptionalOnly pins that {optional: true} keeps an empty (match-anything) selector.
func TestTapOnOptionalOnly(t *testing.T) {
	c := step(t, "- tapOn: {optional: true}\n")
	if c.Selector == nil || c.Selector.HasCriteria() || !c.Optional {
		t.Errorf("%+v", c)
	}
}

// TestTapErrors pins rejected tap options.
func TestTapErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"unknown key", "- tapOn:\n    text: X\n    foo: 1\n", `Unknown Property: "foo" is not an option of tapOn`, 5},
		{"unknown key on doubleTapOn", "- doubleTapOn: {text: X, foo: 1}\n", "not an option of doubleTapOn", 3},
		{"unknown key on longPressOn", "- longPressOn: {text: X, duration: 1}\n", "not an option of longPressOn", 3},
		{"optional not a bool", "- tapOn: {text: X, optional: yes}\n", "expected true or false", 3},
		{"repeat not a number", "- tapOn: {text: X, repeat: many}\n", "expected a number", 3},
		{"delay not a number", "- tapOn: {text: X, delay: soon}\n", "expected a number", 3},
		{"settle not a number", "- tapOn: {text: X, waitToSettleTimeoutMs: x}\n", "expected a number", 3},
		{"retryTapIfNoChange not a bool", "- tapOn: {text: X, retryTapIfNoChange: 2}\n", "expected true or false", 3},
		{"waitUntilVisible not a bool", "- tapOn: {text: X, waitUntilVisible: x}\n", "expected true or false", 3},
		{"list value", "- tapOn: [X]\n", "a selector is a text or a map", 3},
	})
}

// ---- assertions ----

// TestElementAssertions pins assertVisible/assertNotVisible/copyTextFrom forms.
func TestElementAssertions(t *testing.T) {
	for _, name := range []string{"assertVisible", "assertNotVisible", "copyTextFrom"} {
		t.Run(name, func(t *testing.T) {
			c := step(t, "- "+name+": Hello\n")
			if c.Name != name || c.Selector.Text != "Hello" || c.Optional {
				t.Errorf("scalar: %+v", c)
			}
			c = step(t, "- "+name+": {id: msg, label: Check it, optional: true}\n")
			if c.Selector.ID != "msg" || c.Label != "Check it" || !c.Optional {
				t.Errorf("map: %+v", c)
			}
			// Tap options are tolerated on these commands (they share the selector map).
			c = step(t, "- "+name+": {id: msg, point: \"1,1\", repeat: 2, delay: 1, retryTapIfNoChange: true}\n")
			if c.Selector.ID != "msg" {
				t.Errorf("tap options: %+v", c)
			}
			e := parseErr(t, hdr+"- "+name+": {id: msg, bogus: 1}\n")
			if !strings.Contains(e.Msg, `"bogus" is not an option of `+name) {
				t.Errorf("unknown key: %v", e)
			}
			e = parseErr(t, hdr+"- "+name+": {id: msg, optional: often}\n")
			if !strings.Contains(e.Msg, "expected true or false") {
				t.Errorf("bad optional: %v", e)
			}
		})
	}
}

// TestAssertTrue pins the scalar and map forms of assertTrue.
func TestAssertTrue(t *testing.T) {
	c := step(t, "- assertTrue: ${output.count > 2}\n")
	if c.Cond == nil || c.Cond.True == nil || *c.Cond.True != "${output.count > 2}" {
		t.Errorf("scalar: %+v", c.Cond)
	}
	c = step(t, "- assertTrue:\n    condition: ${a == b}\n    label: same\n    optional: true\n")
	if *c.Cond.True != "${a == b}" || c.Label != "same" || !c.Optional {
		t.Errorf("map: %+v %+v", c, c.Cond)
	}
	if c.Mutating() {
		t.Error("assertTrue does not touch the device")
	}
	runErrCases(t, hdr, []errCase{
		{"list", "- assertTrue: [x]\n", "assertTrue takes a condition", 3},
		{"bad optional", "- assertTrue: {condition: x, optional: z}\n", "expected true or false", 3},
	})
}

// TestExtendedWaitUntil pins visible/notVisible/timeout and its errors.
func TestExtendedWaitUntil(t *testing.T) {
	c := step(t, "- extendedWaitUntil:\n    visible: Done\n    timeout: 10000\n    label: wait\n    optional: true\n")
	if c.Cond.Visible.Text != "Done" || c.Cond.NotVisible != nil || c.Timeout != "10000" || c.Label != "wait" || !c.Optional {
		t.Errorf("visible: %+v %+v", c, c.Cond)
	}
	c = step(t, "- extendedWaitUntil:\n    notVisible: {id: spinner, optional: true}\n    timeout: ${T}\n")
	if c.Cond.NotVisible.ID != "spinner" || c.Cond.Visible != nil || c.Timeout != "${T}" {
		t.Errorf("notVisible: %+v", c.Cond)
	}
	c = step(t, "- extendedWaitUntil: {visible: A, notVisible: B}\n")
	if c.Cond.Visible.Text != "A" || c.Cond.NotVisible.Text != "B" || c.Timeout != "" {
		t.Errorf("both: %+v", c.Cond)
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- extendedWaitUntil: Done\n", "extendedWaitUntil takes a map", 3},
		{"neither", "- extendedWaitUntil: {timeout: 1000}\n", "expects either `visible` or `notVisible`", 3},
		{"null visible", "- extendedWaitUntil: {visible: ~}\n", "expects either `visible` or `notVisible`", 3},
		{"unknown key", "- extendedWaitUntil:\n    visible: A\n    timeOut: 5\n", `"timeOut" is not an option of extendedWaitUntil`, 5},
		{"unknown key in visible", "- extendedWaitUntil: {visible: {txt: A}}\n", "is not an option of visible", 3},
		{"unknown key in notVisible", "- extendedWaitUntil: {notVisible: {txt: A}}\n", "is not an option of notVisible", 3},
		{"bad optional", "- extendedWaitUntil: {visible: A, optional: q}\n", "expected true or false", 3},
	})
}

// ---- text ----

// TestInputText pins inputText's forms; the map form ignores unknown keys by design.
func TestInputText(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"- inputText: hello\n", "hello"},
		{"- inputText: 1234\n", "1234"},
		{"- inputText: \"\"\n", ""},
		{"- inputText: ${USER}\n", "${USER}"},
		{"- inputText: {text: abc, label: Type}\n", "abc"},
		{"- inputText: {text: abc, optional: true, whatever: 1}\n", "abc"},
	} {
		c := step(t, tc.body)
		if c.Text != tc.want || !c.LabelRaw {
			t.Errorf("%q: Text %q LabelRaw %v", tc.body, c.Text, c.LabelRaw)
		}
	}
	if c := step(t, "- inputText: {text: abc, label: Type}\n"); c.Label != "Type" {
		t.Errorf("label: %q", c.Label)
	}
	runErrCases(t, hdr, []errCase{{"list", "- inputText: [a]\n", "inputText takes a text", 3}})
}

// TestInputRandom pins every inputRandom* kind, its default length and options.
func TestInputRandom(t *testing.T) {
	for _, kind := range []string{"Text", "Number", "Email", "PersonName", "CityName", "CountryName", "ColorName"} {
		name := "inputRandom" + kind
		t.Run(name, func(t *testing.T) {
			c := step(t, "- "+name+"\n")
			if c.RandomKind != kind || c.Length != 8 {
				t.Errorf("bare: kind %q length %d", c.RandomKind, c.Length)
			}
			c = step(t, "- "+name+": {label: L, optional: true}\n")
			if c.RandomKind != kind || c.Label != "L" || !c.Optional || c.Length != 8 {
				t.Errorf("map: %+v", c)
			}
			e := parseErr(t, hdr+"- "+name+": abc\n")
			if !strings.Contains(e.Msg, name+" takes no value, or a map") {
				t.Errorf("scalar: %v", e)
			}
		})
	}
	for _, tc := range []struct {
		body   string
		length int
	}{
		{"- inputRandomText: {length: 12}\n", 12},
		{"- inputRandomNumber: {length: 4}\n", 4},
		{"- inputRandomText: {length: 0}\n", 8},
		{"- inputRandomNumber: {length: -3}\n", 8},
		{"- inputRandomText: {length: ~}\n", 8},
	} {
		if c := step(t, tc.body); c.Length != tc.length {
			t.Errorf("%q: length %d, want %d", tc.body, c.Length, tc.length)
		}
	}
	runErrCases(t, hdr, []errCase{
		{"length only for Text and Number", "- inputRandomEmail: {length: 4}\n", `"length" is not an option of inputRandomEmail`, 3},
		{"length not a number", "- inputRandomText: {length: long}\n", "expected a number", 3},
		{"unknown key", "- inputRandomNumber: {digits: 3}\n", `"digits" is not an option of inputRandomNumber`, 3},
	})
}

// TestEraseText pins eraseText's default count and its scalar/map forms.
func TestEraseText(t *testing.T) {
	for _, tc := range []struct {
		body  string
		count int
	}{
		{"- eraseText\n", 50},
		{"- eraseText: 10\n", 10},
		{"- eraseText: {charactersToErase: 5, label: L}\n", 5},
		{"- eraseText: {}\n", 50},
		{"- eraseText: {optional: true}\n", 50},
	} {
		if c := step(t, tc.body); c.Count != tc.count {
			t.Errorf("%q: count %d, want %d", tc.body, c.Count, tc.count)
		}
	}
	runErrCases(t, hdr, []errCase{
		{"scalar not a number", "- eraseText: all\n", `expected a number, got "all"`, 3},
		{"map count not a number", "- eraseText: {charactersToErase: x}\n", "expected a number", 3},
		{"unknown key", "- eraseText: {chars: 3}\n", `"chars" is not an option of eraseText`, 3},
	})
}

// TestSetClipboard pins setClipboard's scalar and map forms.
func TestSetClipboard(t *testing.T) {
	if c := step(t, "- setClipboard: secret\n"); c.Text != "secret" || c.Mutating() {
		t.Errorf("scalar: %+v", c)
	}
	if c := step(t, "- setClipboard: {text: abc, label: L, optional: true}\n"); c.Text != "abc" || c.Label != "L" || !c.Optional {
		t.Errorf("map: %+v", c)
	}
	runErrCases(t, hdr, []errCase{{"unknown key", "- setClipboard: {value: x}\n", `"value" is not an option of setClipboard`, 3}})
}

// ---- keys ----

// TestPressKeyNames pins that key names are matched case-insensitively (and trimmed).
func TestPressKeyNames(t *testing.T) {
	for _, k := range []string{"Enter", "enter", "ENTER", "Backspace", "back", "HOME", "lock", "Tab", "escape",
		"Power", "volume up", "Volume Down", "remote dpad center", "Remote Media Play Pause", "TV Input HDMI 3",
		"Remote Button A", "Remote Menu", " Enter "} {
		t.Run(k, func(t *testing.T) {
			c := step(t, fmt.Sprintf("- pressKey: %q\n", k))
			if c.Text != k {
				t.Errorf("Text %q (the raw name is kept)", c.Text)
			}
		})
	}
	c := step(t, "- pressKey: {key: Home, label: go home, optional: true}\n")
	if c.Text != "Home" || c.Label != "go home" || !c.Optional {
		t.Errorf("map: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"underscore name", "- pressKey: VOLUME_UP\n", `Unknown key name "VOLUME_UP"`, 3},
		{"unknown name", "- pressKey: F13\n", `Unknown key name "F13"`, 3},
		{"map without key", "- pressKey: {label: x}\n", `Unknown key name ""`, 3},
		{"unknown map key", "- pressKey: {key: Home, code: 3}\n", `"code" is not an option of pressKey`, 3},
		{"list", "- pressKey: [Home]\n", "Unknown key name", 3},
	})
}

// TestNoValueCommands pins commands that take nothing or {label, optional}.
func TestNoValueCommands(t *testing.T) {
	for _, name := range []string{"back", "hideKeyboard", "pasteText", "scroll", "clearKeychain", "stopRecording",
		"toggleAirplaneMode", "toggleDarkMode", "assertDarkMode", "assertLightMode"} {
		t.Run(name, func(t *testing.T) {
			c := step(t, "- "+name+": {label: L, optional: true}\n")
			if c.Name != name || c.Label != "L" || !c.Optional {
				t.Errorf("map: %+v", c)
			}
			e := parseErr(t, hdr+"- "+name+": now\n")
			if !strings.Contains(e.Msg, name+" takes no value") {
				t.Errorf("scalar: %v", e)
			}
			e = parseErr(t, hdr+"- "+name+": {when: x}\n")
			if !strings.Contains(e.Msg, `"when" is not an option of `+name) {
				t.Errorf("unknown key: %v", e)
			}
		})
	}
}

// TestAction pins the legacy `action:` command, which renames itself to its target.
func TestAction(t *testing.T) {
	for _, target := range []string{"back", "hideKeyboard", "scroll", "clearKeychain", "pasteText"} {
		if c := step(t, "- action: "+target+"\n"); c.Name != target {
			t.Errorf("action %s: Name %q", target, c.Name)
		}
	}
	runErrCases(t, hdr, []errCase{
		{"unknown target", "- action: fly\n", `Unknown navigation target "fly"`, 3},
		{"map", "- action: {back: true}\n", `Unknown navigation target ""`, 3},
	})
}

// ---- swipe and scroll ----

// TestSwipeDirection pins the direction form and its defaults.
func TestSwipeDirection(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{{"UP", "UP"}, {"down", "DOWN"}, {"LEFT", "LEFT"}, {"right", "RIGHT"}} {
		c := step(t, "- swipe: {direction: "+tc.dir+"}\n")
		if c.Direction != tc.want || c.Duration != 400 || c.From != nil || c.Start != "" {
			t.Errorf("%s: %+v", tc.dir, c)
		}
	}
	c := step(t, `- swipe:
    direction: UP
    duration: 1000
    label: "\"Swipe up\""
    optional: TRUE
    waitToSettleTimeoutMs: 300
`)
	if c.Duration != 1000 || c.Label != "Swipe up" || !c.Optional || c.WaitToSettleTimeoutMs != 300 {
		t.Errorf("options (quotes are stripped from the label, as Maestro does): %+v", c)
	}
	if c := step(t, "- swipe: {direction: UP, duration: ~}\n"); c.Duration != 400 {
		t.Errorf("null duration keeps the default: %d", c.Duration)
	}
}

// TestSwipeStartEnd pins the start/end form with percentages or pixels.
func TestSwipeStartEnd(t *testing.T) {
	c := step(t, "- swipe: {start: \"90%,50%\", end: \"10%,50%\"}\n")
	if c.Start != "90%,50%" || c.End != "10%,50%" || c.Direction != "" {
		t.Errorf("percent: %+v", c)
	}
	c = step(t, "- swipe: {start: \"100,200\", end: \"300,400\", duration: 250}\n")
	if c.Start != "100,200" || c.End != "300,400" || c.Duration != 250 {
		t.Errorf("pixels: %+v", c)
	}
}

// TestSwipeFrom pins the from-element form.
func TestSwipeFrom(t *testing.T) {
	c := step(t, "- swipe: {from: {id: card}, direction: LEFT}\n")
	if c.From == nil || c.From.ID != "card" || c.Direction != "LEFT" || c.Optional {
		t.Errorf("map from: %+v", c)
	}
	c = step(t, "- swipe: {from: Card, direction: right, label: L}\n")
	if c.From.Text != "Card" || c.Direction != "RIGHT" || c.Label != "L" {
		t.Errorf("text from: %+v", c)
	}
	c = step(t, "- swipe: {from: {id: card, point: \"10%,10%\", label: inner}, direction: UP}\n")
	if c.Point != "10%,10%" || c.From.ID != "card" {
		t.Errorf("from point: %+v", c)
	}
}

// TestSwipeErrors pins swipe's rejections.
func TestSwipeErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"scalar", "- swipe: UP\n", "swipe takes a map", 3},
		{"empty map", "- swipe: {}\n", "Swipe command takes either: direction, start and end, or from and direction", 3},
		{"from without direction", "- swipe: {from: X}\n", "Swipe command takes either", 3},
		{"mixed case direction", "- swipe: {direction: Up}\n", "all upper or all lower case", 3},
		{"bad direction", "- swipe:\n    direction: SIDEWAYS\n", "swipe direction must be UP, DOWN, LEFT or RIGHT", 4},
		{"bad direction with from", "- swipe: {from: X, direction: north}\n", "swipe direction must be UP, DOWN, LEFT or RIGHT", 3},
		{"start without end", "- swipe: {start: \"90%,50%\"}\n", "swipe needs both start and end", 3},
		{"end without start", "- swipe: {end: \"90%,50%\"}\n", "swipe needs both start and end", 3},
		{"start/end and direction", "- swipe: {start: \"1,1\", end: \"2,2\", direction: UP}\n", "either direction or start/end, not both", 3},
		{"percent mixed with pixels", "- swipe: {start: \"90%,50%\", end: \"100,200\"}\n", "both be percentages or both be coordinates", 3},
		{"duration not a number", "- swipe: {direction: UP, duration: slow}\n", "expected a number", 3},
		{"unknown key with from", "- swipe: {from: X, direction: UP, speed: 3}\n", `"speed" is not an option of swipe`, 3},
		{"unknown key inside from", "- swipe: {from: {id: x, bogus: 1}, direction: UP}\n", `"bogus" is not an option of from`, 3},
		{"bad from selector", "- swipe: {from: [x], direction: UP}\n", "a selector is a text or a map", 3},
	})
}

// TestScrollUntilVisible pins scrollUntilVisible's fields, defaults and clamps.
func TestScrollUntilVisible(t *testing.T) {
	c := step(t, "- scrollUntilVisible: {element: Footer}\n")
	if c.Selector.Text != "Footer" || c.Direction != "DOWN" || c.VisibilityPct != 100 || c.CenterElement ||
		c.Timeout != "" || c.Speed != "" || c.Optional {
		t.Errorf("defaults: %+v", c)
	}
	c = step(t, `- scrollUntilVisible:
    element:
      id: row_42
    direction: up
    timeout: 20000
    speed: 40
    visibilityPercentage: 50
    centerElement: true
    waitToSettleTimeoutMs: 200
    label: find row
    optional: true
`)
	if c.Selector.ID != "row_42" || c.Direction != "UP" || c.Timeout != "20000" || c.Speed != "40" ||
		c.VisibilityPct != 50 || !c.CenterElement || c.WaitToSettleTimeoutMs != 200 || c.Label != "find row" || !c.Optional {
		t.Errorf("all fields: %+v", c)
	}
	for _, tc := range []struct {
		pct  string
		want int
	}{{"150", 100}, {"-5", 0}, {"0", 0}, {"75", 75}} {
		if c := step(t, "- scrollUntilVisible: {element: X, visibilityPercentage: "+tc.pct+"}\n"); c.VisibilityPct != tc.want {
			t.Errorf("visibility %s: %d, want %d", tc.pct, c.VisibilityPct, tc.want)
		}
	}
	for _, d := range []string{"LEFT", "right", "Down"} {
		if c := step(t, "- scrollUntilVisible: {element: X, direction: "+d+"}\n"); c.Direction != strings.ToUpper(d) {
			t.Errorf("direction %s: %q", d, c.Direction)
		}
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- scrollUntilVisible: Footer\n", "scrollUntilVisible takes a map with element", 3},
		{"no element", "- scrollUntilVisible: {direction: UP}\n", "scrollUntilVisible needs element", 3},
		{"null element", "- scrollUntilVisible: {element: ~}\n", "scrollUntilVisible needs element", 3},
		{"bad direction", "- scrollUntilVisible:\n    element: X\n    direction: diagonal\n", "direction must be UP, DOWN, LEFT or RIGHT", 5},
		{"bad visibility", "- scrollUntilVisible: {element: X, visibilityPercentage: most}\n", "expected a number", 3},
		{"bad centerElement", "- scrollUntilVisible: {element: X, centerElement: 2}\n", "expected true or false", 3},
		{"unknown key", "- scrollUntilVisible: {element: X, maxScrolls: 3}\n", `"maxScrolls" is not an option of scrollUntilVisible`, 3},
		{"unknown key in element", "- scrollUntilVisible: {element: {txt: X}}\n", `"txt" is not an option of element`, 3},
	})
}

// TestCommandLinesAndFiles pins that each command records its own line and file.
func TestCommandLinesAndFiles(t *testing.T) {
	f := mustParse(t, hdr+"- launchApp\n\n- tapOn:\n    text: X\n# comment\n- back\n")
	var got []int
	for _, c := range f.Commands {
		got = append(got, c.Line)
		if c.File != f.Path {
			t.Errorf("File %q", c.File)
		}
	}
	if !reflect.DeepEqual(got, []int{3, 5, 8}) {
		t.Errorf("lines %v", got)
	}
}
