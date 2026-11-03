package js

import (
	"testing"

	"github.com/elforama/testbuddy/internal/legacy"
)

func TestInterpolate(t *testing.T) {
	e := New("android")
	env := &Scope{Env: map[string]string{"USER": "ann", "N": "1"}, Platform: "android"}
	cases := map[string]string{
		"Hi ${USER}!":                 "Hi ann!",
		"${MISSING || 'dflt'}":        "dflt",
		"${N + 1}":                    "11",
		"\\${USER}":                   "${USER}",
		"${tb.platform}":              "android",
		"${[1,2]}":                    "[1, 2]",
		"a ${USER} and ${N}":          "a ann and 1",
		"${relativePoint(0.5, 0.25)}": "50%,25%",
	}
	for in, want := range cases {
		got, err := e.Interpolate(in, env)
		if err != nil || got != want {
			t.Errorf("%q -> %q (%v), want %q", in, got, err, want)
		}
	}
	// output persists, implicit globals and env do not
	if _, err := e.Eval("output.count = 2", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Interpolate("${output.count * 2}", nil); got != "4" {
		t.Errorf("output: %q", got)
	}
	if got, _ := e.Interpolate("${typeof USER}", nil); got != "undefined" {
		t.Errorf("env leaked: %q", got)
	}
	if err := e.RunScript("var a = 1; output.a = a + json('{\"b\":2}').b", "", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Interpolate("${output.a}", nil); got != "3" {
		t.Errorf("runScript: %q", got)
	}
	if _, err := e.Eval("syntax error here", nil); err == nil {
		t.Errorf("expected a JS error")
	}
}

func TestTruthy(t *testing.T) {
	for s, want := range map[string]bool{"": false, "false": false, "FALSE": false, "0": false, "0.0": false,
		"undefined": false, "null": false, "true": true, "NaN": true, "no": true, "false false": true} {
		if Truthy(s) != want {
			t.Errorf("Truthy(%q) != %v", s, want)
		}
	}
}

// Flows written for the earlier runner reach the same object under its legacy name.
func TestLegacyScriptObject(t *testing.T) {
	e := New("ios")
	copied := "hello"
	sc := &Scope{Platform: "ios", CopiedText: &copied}
	expr := "${" + legacy.ScriptObject() + ".copiedText + ' ' + " + legacy.ScriptObject() + ".platform}"
	if got, err := e.Interpolate(expr, sc); err != nil || got != "hello ios" {
		t.Errorf("legacy object: %q (%v)", got, err)
	}
	if got, _ := e.Interpolate("${tb.copiedText}", sc); got != "hello" {
		t.Errorf("tb.copiedText: %q", got)
	}
}
