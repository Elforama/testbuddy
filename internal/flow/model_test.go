package flow

import (
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMutating pins which commands count as device interactions for the lookup-timeout clock.
func TestMutating(t *testing.T) {
	readOnly := []string{"assertVisible", "assertNotVisible", "assertTrue", "extendedWaitUntil", "assertScreenshot",
		"assertWithAI", "assertNoDefectsWithAI", "extractTextWithAI", "copyTextFrom", "setClipboard",
		"clearKeychain", "setPermissions", "takeScreenshot", "startRecording", "stopRecording",
		"assertDarkMode", "assertLightMode", "defineVariables", "signal", "waitForSignal",
		"runFlow", "repeat", "retry"} // composites: their commands count for themselves
	for _, n := range readOnly {
		if (&Command{Name: n}).Mutating() {
			t.Errorf("%s should not be mutating", n)
		}
	}
	for _, n := range []string{"tapOn", "doubleTapOn", "longPressOn", "inputText", "inputRandomText", "eraseText",
		"pasteText", "swipe", "scroll", "scrollUntilVisible", "back", "hideKeyboard", "pressKey", "launchApp",
		"stopApp", "killApp", "clearState", "openLink", "setLocation", "travel",
		"setOrientation", "setAirplaneMode", "toggleDarkMode", "waitForAnimationToEnd", "on", "parallel", ""} {
		if !(&Command{Name: n}).Mutating() {
			t.Errorf("%s should be mutating", n)
		}
	}
	// A parsed `action: back` becomes back, which mutates.
	if c := step(t, "- action: back\n"); !c.Mutating() {
		t.Error("action: back")
	}
}

// TestNormalizeOrientation pins every accepted orientation spelling.
func TestNormalizeOrientation(t *testing.T) {
	for in, want := range map[string]string{
		"PORTRAIT": "PORTRAIT", "portrait": "PORTRAIT", "Portrait": "PORTRAIT",
		"LANDSCAPE_LEFT": "LANDSCAPE_LEFT", "landscapeLeft": "LANDSCAPE_LEFT", "landscape_left": "LANDSCAPE_LEFT",
		"LANDSCAPE_RIGHT": "LANDSCAPE_RIGHT", "LandscapeRight": "LANDSCAPE_RIGHT",
		"UPSIDE_DOWN": "UPSIDE_DOWN", "upsideDown": "UPSIDE_DOWN", "UPSIDEDOWN": "UPSIDE_DOWN",
		"": "", "LANDSCAPE": "", "sideways": "", "landscape-left": "", " PORTRAIT": "",
	} {
		if got := NormalizeOrientation(in); got != want {
			t.Errorf("NormalizeOrientation(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValidKey pins pressKey's key list: every name in any case, nothing else.
func TestValidKey(t *testing.T) {
	for _, k := range keyNames {
		for _, v := range []string{k, strings.ToLower(k), strings.ToUpper(k), "  " + k + "\t"} {
			if !validKey(v) {
				t.Errorf("validKey(%q) = false", v)
			}
		}
	}
	for _, k := range []string{"", "VOLUME_UP", "VolumeUp", "Enterr", "F1", "Space", "Remote", "TV Input HDMI 4"} {
		if validKey(k) {
			t.Errorf("validKey(%q) = true", k)
		}
	}
}

// TestLevenshtein pins the edit distance behind "Did you mean" suggestions.
func TestLevenshtein(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"abc", "abc", 0}, {"", "abc", 3}, {"abc", "", 3}, {"kitten", "sitting", 3},
		{"tapon", "tapOn", 1}, {"héllo", "hello", 1}, {"flaw", "lawn", 2}, {"ab", "ba", 2},
	} {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestSuggest pins suggestions: case-insensitive, only within 3 edits.
func TestSuggest(t *testing.T) {
	for in, want := range map[string]string{
		"tapOnn":       ". Did you mean tapOn?",
		"TAPON":        ". Did you mean tapOn?",
		"lauchApp":     ". Did you mean launchApp?",
		"inputtext":    ". Did you mean inputText?",
		"swip":         ". Did you mean swipe?",
		"assertVisble": ". Did you mean assertVisible?",
		"zzzzzzzzzzzz": "",
		"":             "",
	} {
		if got := suggest(in); got != want {
			t.Errorf("suggest(%q) = %q, want %q", in, got, want)
		}
	}
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Line: 7}
}

// TestParseInt pins number parsing: underscores, spaces, floats truncate, errors carry the line.
func TestParseInt(t *testing.T) {
	p := &parser{file: "x.yaml"}
	for in, want := range map[string]int{"42": 42, " 7 ": 7, "1_000": 1000, "2.9": 2, "-3": -3, "1e3": 1000, "0": 0} {
		got, err := p.parseInt(scalarNode(in))
		if err != nil || got != want {
			t.Errorf("parseInt(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1,000", "ten"} {
		_, err := p.parseInt(scalarNode(in))
		e := asFlowErr(t, err)
		if e.Line != 7 || e.File != "x.yaml" || !strings.Contains(e.Msg, "expected a number") {
			t.Errorf("parseInt(%q): %v", in, e)
		}
	}
}

// TestParseBool pins bool parsing: only true/false, in any case.
func TestParseBool(t *testing.T) {
	p := &parser{file: "x.yaml"}
	for in, want := range map[string]bool{"true": true, "TRUE": true, "True": true, "false": false, "False": false, "FALSE": false} {
		got, err := p.parseBool(scalarNode(in))
		if err != nil || got != want {
			t.Errorf("parseBool(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "yes", "no", "1", "0", "on", "t"} {
		_, err := p.parseBool(scalarNode(in))
		if e := asFlowErr(t, err); e.Line != 7 || !strings.Contains(e.Msg, "expected true or false") {
			t.Errorf("parseBool(%q): %v", in, e)
		}
	}
}

// TestNodeHelpers pins isNull and scalarOr.
func TestNodeHelpers(t *testing.T) {
	null := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "~"}
	str := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x"}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	if !isNull(nil) || !isNull(null) || isNull(str) || isNull(seq) {
		t.Error("isNull")
	}
	if scalarOr(nil, "d") != "d" || scalarOr(null, "d") != "d" || scalarOr(seq, "d") != "d" || scalarOr(str, "d") != "x" {
		t.Error("scalarOr")
	}
}

// TestBaseNoExt pins the name helper used by Flow.Name.
func TestBaseNoExt(t *testing.T) {
	for in, want := range map[string]string{"/a/b/c.yaml": "c", "c.yml": "c", "/a/b.c/d": "d", "x.test.yaml": "x.test"} {
		if got := baseNoExt(in); got != want {
			t.Errorf("baseNoExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOptionalMustBeBool pins that every map form validates `optional`.
func TestOptionalMustBeBool(t *testing.T) {
	dir := writeFiles(t, map[string]string{"s.js": "1", "sub.yaml": hdr + "- back\n", "pic.png": "x"})
	for _, body := range []string{
		"tapOn: {text: X, optional: z}",
		"assertVisible: {text: X, optional: z}",
		"inputRandomText: {optional: z}",
		"eraseText: {optional: z}",
		"setClipboard: {text: x, optional: z}",
		"scrollUntilVisible: {element: X, optional: z}",
		"back: {optional: z}",
		"pressKey: {key: Home, optional: z}",
		"launchApp: {optional: z}",
		"stopApp: {optional: z}",
		"setPermissions: {optional: z}",
		"openLink: {link: x, optional: z}",
		"runFlow: {file: sub.yaml, optional: z}",
		"runScript: {file: s.js, optional: z}",
		"repeat: {times: 1, commands: [back], optional: z}",
		"retry: {commands: [back], optional: z}",
		"takeScreenshot: {path: p, optional: z}",
		"waitForAnimationToEnd: {optional: z}",
		"setLocation: {latitude: 1, longitude: 2, optional: z}",
		"travel: {points: [\"1,2\"], optional: z}",
		"addMedia: {files: [pic.png], optional: z}",
		"extendedWaitUntil: {visible: X, optional: z}",
		"waitForSignal: {name: x, optional: z}",
	} {
		name, _, _ := strings.Cut(body, ":")
		t.Run(name, func(t *testing.T) {
			_, err := ParseBytes(filepath.Join(dir, "main.yaml"), []byte(hdr+"- "+body+"\n"))
			if e := asFlowErr(t, err); !strings.Contains(e.Msg, `expected true or false, got "z"`) || e.Line != 3 {
				t.Errorf("%v", e)
			}
		})
	}
}

// TestMoreValueErrors pins a few remaining bad-value paths.
func TestMoreValueErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"assertVisible bad selector value", "- assertVisible: {width: x}\n", "expected a number", 3},
		{"scrollUntilVisible bad settle", "- scrollUntilVisible: {element: X, waitToSettleTimeoutMs: x}\n", "expected a number", 3},
		{"alias as a command", "- &b back\n- *b\n", "each command is a string or a single-key map", 4},
	})
	runErrCases(t, mdHdr, []errCase{
		{"alias as a role block", "- bob: &b [back]\n- alice: *b\n", `expected commands for device "alice"`, 6},
	})
}

// TestCommandDefault pins command()'s guard for names the switch does not know.
func TestCommandDefault(t *testing.T) {
	p := &parser{file: "x.yaml"}
	_, err := p.command("nope", nil, scalarNode("nope"))
	if e := asFlowErr(t, err); e.Msg != `Invalid Command: "nope"` || e.Line != 7 {
		t.Errorf("%v", e)
	}
}

// TestMapViewHas pins mapView.has.
func TestMapViewHas(t *testing.T) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("a: 1\nb: ~\n"), &n); err != nil {
		t.Fatal(err)
	}
	m := (&parser{}).mapOf(n.Content[0])
	if !m.has("a") || !m.has("b") || m.has("c") {
		t.Error("has")
	}
}
