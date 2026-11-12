package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
)

// parse writes the flow (and any other files) to a temp folder and parses it.
func parse(t *testing.T, src string, files ...string) *flow.Flow {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i+1 < len(files); i += 2 {
		if err := os.WriteFile(filepath.Join(dir, files[i]), []byte(files[i+1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "flow.yaml")
	f, err := flow.ParseBytes(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// codes renders warnings as "code@line" ("code" alone for flow-wide ones).
func codes(ws []Warning) string {
	var out []string
	for _, w := range ws {
		if w.Line == 0 {
			out = append(out, w.Code)
		} else {
			out = append(out, w.Code+"@"+itoa(w.Line))
		}
	}
	return strings.Join(out, " ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

const header = "appId: com.app\n---\n"

func TestRules(t *testing.T) {
	cases := []struct {
		name, flow, want string
	}{
		{"clean flow", `- launchApp
- tapOn: "Sign in"
- tapOn: {id: email}
- assertVisible: Welcome`, ""},
		{"coordinate tap, percent", `- tapOn: {point: "50%,20%"}
- assertVisible: x`, "coordinate_tap@3"},
		{"coordinate tap, pixels", `- doubleTapOn: {point: "100,200"}
- longPressOn: {point: "10%,10%"}
- assertVisible: x`, "coordinate_tap@3 coordinate_tap@4"},
		{"coordinate swipe", `- swipe: {start: "10%,50%", end: "90%,50%"}
- swipe: {start: "10,50", end: "90,50"}
- swipe: {direction: UP}
- swipe: {from: {point: "50%,50%"}, direction: UP}
- assertVisible: x`, "coordinate_swipe@3 coordinate_swipe@4 coordinate_swipe@6"},
		{"indexed selector, nested relation too", `- tapOn: {text: OK, index: 1}
- tapOn: {text: Buy, below: {text: Price, index: 2}}
- assertVisible: x`, "indexed_selector@3 indexed_selector@4"},
		{"regex text", `- tapOn: "Price ($)"
- tapOn: "Sign in?"
- assertVisible: "(Optional)"
- tapOn: {id: "save.btn?"}`, "regex_text@3 regex_text@4 regex_text@5 regex_text@6"},
		{"intended or harmless regex", `- tapOn: ".*Settings.*"
- tapOn: "Item [0-9]+"
- tapOn: "Hello."
- tapOn: "Yes|No"
- tapOn: "^Start"
- tapOn: "Total \\$5"
- tapOn: "Price: ${PRICE}"
- tapOn: "Hello (world"
- assertVisible: x`, ""},
		{"optional assertion", `- assertVisible: {text: Done, optional: true}
- assertNotVisible: {text: Spinner, optional: true}
- extendedWaitUntil: {visible: Done, timeout: 1000, optional: true}
- assertTrue: {condition: "${ok}", optional: true}
- tapOn: {text: Maybe, optional: true}`, "optional_assertion@3 optional_assertion@4 optional_assertion@5 optional_assertion@6"},
		{"missing assertion", `- launchApp
- tapOn: Go`, "missing_assertion"},
		{"extendedWaitUntil counts as an assertion", `- extendedWaitUntil: {visible: Done, timeout: 1000}`, ""},
		{"assertTrue counts as an assertion", `- assertTrue: "${1 == 1}"`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codes(Flow(parse(t, header+c.flow)))
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// Inline nested commands are linted, and their assertions count; a runFlow file is linted
// on its own, not through the flows that run it.
func TestNested(t *testing.T) {
	f := parse(t, header+`- repeat:
    times: 2
    commands:
      - tapOn: {point: "1,1"}
- retry:
    maxRetries: 1
    commands:
      - tapOn: {text: X, index: 1}
- runFlow:
    when: {visible: {text: "Promo?"}}
    commands:
      - assertVisible: Done
- runFlow: sub.yaml
`, "sub.yaml", "appId: com.app\n---\n- tapOn: {point: \"5,5\"}\n")
	got := codes(Flow(f))
	want := "coordinate_tap@6 indexed_selector@10 regex_text@11"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// Commands inside a block have no step; the runFlow's own condition is step 3's.
	steps := []int{}
	for _, w := range Flow(f) {
		steps = append(steps, w.Step)
	}
	if len(steps) != 3 || steps[0] != 0 || steps[1] != 0 || steps[2] != 3 {
		t.Errorf("steps %v, want [0 0 3]", steps)
	}
}

// Multi-device role blocks and parallel blocks are walked.
func TestMultiDevice(t *testing.T) {
	f := parse(t, `devices:
  alice: {platform: ios, appId: com.chat}
  bob: {platform: android, appId: com.chat}
---
- parallel:
    alice: [{tapOn: {point: "1,1"}}]
    bob: [launchApp]
- bob:
    - assertVisible: {text: Hi, index: 1}
`)
	got := codes(Flow(f))
	if got != "coordinate_tap@6 indexed_selector@9" {
		t.Errorf("got %q", got)
	}
}

// Top-level warnings carry their 1-based step; onFlowStart commands are linted too.
func TestStepsAndHooks(t *testing.T) {
	f := parse(t, `appId: com.app
onFlowStart:
  - tapOn: {point: "1,1"}
---
- launchApp
- tapOn: {text: OK, index: 1}
- assertVisible: x
`)
	ws := Flow(f)
	if codes(ws) != "coordinate_tap@3 indexed_selector@6" {
		t.Fatalf("got %q", codes(ws))
	}
	if ws[0].Step != 0 || ws[1].Step != 2 {
		t.Errorf("steps %d, %d: want 0 (a hook), 2", ws[0].Step, ws[1].Step)
	}
	if !strings.HasSuffix(ws[1].File, "flow.yaml") {
		t.Errorf("file %q", ws[1].File)
	}
}

// Messages say what is wrong and how to fix it; pixel taps say they break across screens,
// and a regex_text warning shows the escaped text.
func TestMessages(t *testing.T) {
	ws := Flow(parse(t, header+`- tapOn: {point: "50%,50%"}
- tapOn: {point: "100,200"}
- tapOn: "Price ($)"
- tapOn: {id: "save.btn?"}
- assertVisible: x`))
	if len(ws) != 4 {
		t.Fatalf("got %+v", ws)
	}
	// A text still finds itself as written (texts also match literally), but matches more;
	// an id does not find itself at all.
	if !strings.Contains(ws[2].Message, "also") {
		t.Errorf("regex text: %q", ws[2].Message)
	}
	if !strings.Contains(ws[3].Message, "never") || !strings.Contains(ws[3].Message, `'save\.btn\?'`) {
		t.Errorf("regex id: %q", ws[3].Message)
	}
	if ws[0].Message != "Tap depends on coordinates. Prefer an element selector." {
		t.Errorf("percent tap: %q", ws[0].Message)
	}
	if !strings.Contains(ws[1].Message, "pixels") || !strings.Contains(ws[1].Message, "screen size") {
		t.Errorf("pixel tap: %q", ws[1].Message)
	}
	if !strings.Contains(ws[2].Message, `'Price \(\$\)'`) {
		t.Errorf("regex text: %q", ws[2].Message)
	}
}

// The escaped form a regex_text warning suggests matches the value as a regex alone, so it
// works for ids too.
func TestSuggestedEscapeMatches(t *testing.T) {
	for _, v := range []string{"Price ($)", "Sign in?", "(Optional)", "save.btn?", "c++", "a{b}"} {
		if esc := regexp.QuoteMeta(v); !match.RegexMatches(esc, v) {
			t.Errorf("%q escaped as %q does not match it", v, esc)
		}
	}
}

// Issue #64: an unknown header key (a mistyped tags:) is kept and the flow still parses and
// runs, but --lint warns about it, naming the known key it is closest to.
func TestUnknownHeaderKey(t *testing.T) {
	f := parse(t, `appId: com.app
name: Login
tgas: [smoke]
jsEngine: graaljs
androidWebViewHierarchy: devtools
properties: {team: core}
env: {USER: a}
myToolSetting: 1
---
- assertVisible: x
`)
	ws := Flow(f)
	if got := codes(ws); got != "unknown_header_key@3 unknown_header_key@8" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(ws[0].Message, `"tgas"`) || !strings.Contains(ws[0].Message, "Did you mean tags?") {
		t.Errorf("near miss: %q", ws[0].Message)
	}
	if strings.Contains(ws[1].Message, "Did you mean") {
		t.Errorf("no near key, no suggestion: %q", ws[1].Message)
	}
	for _, near := range []string{"tag", "Tags", "tagz"} {
		ws := Flow(parse(t, "appId: com.app\n"+near+": [smoke]\n---\n- assertVisible: x\n"))
		if len(ws) != 1 || !strings.Contains(ws[0].Message, "Did you mean tags?") {
			t.Errorf("%s: %+v", near, ws)
		}
	}
}

// Every header key tb or Maestro reads gets no warning: a valid flow's header stays quiet.
func TestKnownHeaderKeys(t *testing.T) {
	f := parse(t, `appId: com.app
url: https://example.com
name: All keys
tags: [smoke]
env: {A: b}
properties: {team: core}
jsEngine: graaljs
androidWebViewHierarchy: devtools
onFlowStart: [launchApp]
onFlowComplete: [back]
---
- assertVisible: x
`)
	if got := codes(Flow(f)); got != "" {
		t.Errorf("got %q", got)
	}
	multi := parse(t, `devices:
  alice: {platform: ios, appId: com.chat}
---
- alice: [{assertVisible: x}]
`)
	if got := codes(Flow(multi)); got != "" {
		t.Errorf("multi-device header: got %q", got)
	}
}

// Issue #64: a key written twice in a map is accepted (the last one wins), in a command or
// in the header, so --lint warns at the second one.
func TestDuplicateKey(t *testing.T) {
	f := parse(t, `appId: com.app
tags: [a]
tags: [b]
---
- tapOn:
    id: email
    id: password
- tapOn: {text: OK, text: Cancel}
- repeat:
    times: 2
    commands:
      - assertVisible:
          text: x
          text: y
`)
	ws := Flow(f)
	if got := codes(ws); got != "duplicate_key@3 duplicate_key@7 duplicate_key@8 duplicate_key@14" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(ws[1].Message, `"id"`) || !strings.Contains(ws[1].Message, "line 6") {
		t.Errorf("message: %q", ws[1].Message)
	}
}
