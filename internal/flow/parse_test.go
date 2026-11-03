package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parse(t *testing.T, src string) (*Flow, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "flow.yaml")
	os.WriteFile(p, []byte(src), 0o644)
	return ParseFile(p)
}

func TestBasics(t *testing.T) {
	f, err := parse(t, `appId: com.x
env:
  A: ${A || 'x'}
---
- launchApp
- tapOn: Login
- tapOn:
    id: submit
    index: 1
    optional: true
- inputText: 1234
- back
- hide keyboard
- swipe:
    direction: UP
- scrollUntilVisible:
    element: Footer
- pressKey: volume up
- assertVisible:
    text: Hi
    below: {id: header}
`)
	if err != nil {
		t.Fatal(err)
	}
	if f.Config.AppID != "com.x" || len(f.Config.Env) != 1 {
		t.Fatalf("config: %+v", f.Config)
	}
	names := []string{}
	for _, c := range f.Commands {
		names = append(names, c.Name)
	}
	want := "launchApp tapOn tapOn inputText back hideKeyboard swipe scrollUntilVisible pressKey assertVisible"
	if strings.Join(names, " ") != want {
		t.Fatalf("got %v", names)
	}
	if c := f.Commands[2]; c.Selector.ID != "submit" || c.Selector.Index != "1" || !c.Optional {
		t.Fatalf("tapOn map: %+v", c.Selector)
	}
	if f.Commands[3].Text != "1234" {
		t.Fatalf("inputText number: %q", f.Commands[3].Text)
	}
	if f.Commands[9].Selector.Below.ID != "header" {
		t.Fatal("nested selector")
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"- launchApp": "Config Section Required",
		"appId: a\n---\n- tapOn: X\n  optional: true": "unexpected top-level field",
		"appId: a\n---\n- tapOn":                      "Missing Command Options",
		"appId: a\n---\n- back:":                      "requires additional options",
		"appId: a\n---\n- tapOnn: X":                  "Did you mean tapOn",
		"appId: a\n---\n- tapOn: {txt: X}":            "Unknown Property",
		"appId: a\n---\n- pressKey: VOLUME_UP":        "Unknown key name",
		"name: x\n---\n- back":                        "appId",
		"appId: a\nenv:\n  K:\n---\n- back":           "has no value",
		"appId: a\n---\n- swipe: {direction: Up}":     "upper or all lower",
	}
	for src, want := range cases {
		_, err := parse(t, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
}

func TestMultiDevice(t *testing.T) {
	f, err := parse(t, `devices:
  alice: ios
  bob:
    platform: android
    appId: com.b
---
- parallel:
    alice:
      - launchApp: com.a
      - signal: ready
    bob:
      - waitForSignal: ready
- bob:
    - tapOn: Hi
- alice: {assertVisible: Hi}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Config.Devices) != 2 || f.Config.Devices[0].Platform != "ios" || f.Config.Devices[1].AppID != "com.b" {
		t.Fatalf("devices: %+v", f.Config.Devices)
	}
	p := f.Commands[0]
	if p.Name != "parallel" || len(p.Parallel["alice"]) != 2 || p.Parallel["bob"][0].Signal != "ready" {
		t.Fatalf("parallel: %+v", p)
	}
	if f.Commands[1].On != "bob" || f.Commands[2].On != "alice" || f.Commands[2].Commands[0].Name != "assertVisible" {
		t.Fatalf("role blocks: %+v %+v", f.Commands[1], f.Commands[2])
	}
	if _, err := parse(t, "devices:\n  tapOn: ios\n---\n- back\n"); err == nil {
		t.Error("a role named like a command should be rejected")
	}
}

// TestCorpus parses every flow in a folder of real-world flows, when one is given
// (TB_FLOW_CORPUS=/path/to/flows).
func TestCorpus(t *testing.T) {
	root := os.Getenv("TB_FLOW_CORPUS")
	if root == "" {
		t.Skip("set TB_FLOW_CORPUS to a folder of flows")
	}
	files, _ := filepath.Glob(filepath.Join(root, "*.yaml"))
	if len(files) == 0 {
		t.Skip("no corpus")
	}
	for _, f := range files {
		if _, err := ParseFile(f); err != nil {
			t.Errorf("%v", err)
		}
	}
}
