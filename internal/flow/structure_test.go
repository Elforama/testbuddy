package flow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// parseIn writes files into a temp dir and parses main.yaml from it.
func parseIn(t *testing.T, files map[string]string) (string, *Flow, error) {
	t.Helper()
	dir := writeFiles(t, files)
	f, err := ParseFile(filepath.Join(dir, "main.yaml"))
	return dir, f, err
}

// ---- conditions ----

// TestConditionAllFields pins every key of a `when` condition.
func TestConditionAllFields(t *testing.T) {
	c := step(t, `- runFlow:
    when:
      visible: Accept
      notVisible: {id: spinner}
      true: ${output.ready}
      platform: iOS
      label: only when ready
      optional: true
      timeout: 3000
    commands:
      - tapOn: Accept
`)
	w := c.When
	if w == nil || w.Visible.Text != "Accept" || w.NotVisible.ID != "spinner" || w.True == nil || *w.True != "${output.ready}" {
		t.Fatalf("condition: %+v", w)
	}
	if w.Platform != "ios" || w.Label != "only when ready" || w.Timeout != "3000" {
		t.Errorf("platform/label/timeout: %+v", w)
	}
}

// TestConditionPlatforms pins platform names (case-insensitive) and their rejection.
func TestConditionPlatforms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Android", "android"}, {"ANDROID", "android"}, {"iOS", "ios"}, {"ios", "ios"}, {"Web", "web"}, {"~", ""},
	} {
		c := step(t, "- runFlow:\n    when: {platform: "+tc.in+"}\n    commands: [back]\n")
		if c.When.Platform != tc.want {
			t.Errorf("%s: %q", tc.in, c.When.Platform)
		}
	}
	e := parseErr(t, hdr+"- runFlow:\n    when:\n      platform: windows\n    commands: [back]\n")
	if !strings.Contains(e.Msg, "platform must be Android, iOS or Web") || e.Line != 5 {
		t.Errorf("%v", e)
	}
}

// TestConditionUnsetFields pins that absent condition parts stay nil.
func TestConditionUnsetFields(t *testing.T) {
	c := step(t, "- runFlow:\n    when: {visible: X, notVisible: ~}\n    commands: [back]\n")
	if c.When.NotVisible != nil || c.When.True != nil || c.When.Platform != "" {
		t.Errorf("%+v", c.When)
	}
	if c := step(t, "- runFlow:\n    when: ~\n    commands: [back]\n"); c.When != nil {
		t.Errorf("null when: %+v", c.When)
	}
}

// TestConditionErrors pins rejected conditions.
func TestConditionErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"scalar", "- runFlow:\n    when: visible\n    commands: [back]\n", "a condition is a map (visible, notVisible, true, platform)", 4},
		{"unknown key", "- runFlow:\n    when:\n      visibel: X\n    commands: [back]\n", `"visibel" is not an option of condition`, 5},
		{"unknown key in visible", "- runFlow:\n    when: {visible: {txt: X}}\n    commands: [back]\n", `"txt" is not an option of visible`, 4},
		{"unknown key in notVisible", "- runFlow:\n    when: {notVisible: {txt: X}}\n    commands: [back]\n", `"txt" is not an option of notVisible`, 4},
		{"bad selector value", "- runFlow:\n    when: {visible: {width: x}}\n    commands: [back]\n", "expected a number", 4},
		{"repeat while scalar", "- repeat:\n    while: visible\n    commands: [back]\n", "a condition is a map", 4},
	})
}

// ---- runFlow ----

// TestRunFlowFile pins file sub-flows: scalar and map forms, env, when, nested relative paths.
func TestRunFlowFile(t *testing.T) {
	dir, f, err := parseIn(t, map[string]string{
		"main.yaml": hdr + `- runFlow: sub.yaml
- runFlow:
    file: sub.yaml
    label: login
    optional: true
    env:
      USER: bob
      PASS: secret
    when:
      platform: Android
- runFlow: nested/inner.yaml
`,
		"sub.yaml":          hdr + "- tapOn: Sub\n",
		"nested/inner.yaml": hdr + "- runFlow: leaf.yaml\n",
		"nested/leaf.yaml":  "appId: com.leaf\nname: Leaf\n---\n- back\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	c0, c1, c2 := f.Commands[0], f.Commands[1], f.Commands[2]
	sub := filepath.Join(dir, "sub.yaml")
	if c0.SubFile != sub || c0.SubFlow == nil || c0.SubFlow.Path != sub || c0.SubFlow.Dir != dir {
		t.Errorf("scalar runFlow: %+v", c0)
	}
	if sc := c0.SubFlow.Commands[0]; sc.Selector.Text != "Sub" || sc.File != sub || sc.Line != 3 {
		t.Errorf("sub-flow command keeps its own file and line: %s:%d", sc.File, sc.Line)
	}
	if c1.SubFile != sub || c1.Label != "login" || !c1.Optional || c1.When.Platform != "android" {
		t.Errorf("map runFlow: %+v", c1)
	}
	if !reflect.DeepEqual(c1.Env, []KV{{"USER", "bob"}, {"PASS", "secret"}}) {
		t.Errorf("env: %v", c1.Env)
	}
	inner := c2.SubFlow
	if c2.SubFile != filepath.Join(dir, "nested", "inner.yaml") || inner == nil {
		t.Fatalf("nested: %+v", c2)
	}
	leaf := inner.Commands[0]
	if leaf.SubFile != filepath.Join(dir, "nested", "leaf.yaml") {
		t.Errorf("a sub-flow's paths resolve against its own folder: %q", leaf.SubFile)
	}
	if leaf.SubFlow.Name() != "Leaf" || leaf.SubFlow.Config.AppID != "com.leaf" || leaf.SubFlow.Commands[0].Name != "back" {
		t.Errorf("leaf: %+v", leaf.SubFlow)
	}
}

// TestRunFlowAbsolutePath pins that an absolute runFlow path is used as is.
func TestRunFlowAbsolutePath(t *testing.T) {
	other := writeFiles(t, map[string]string{"shared.yaml": hdr + "- back\n"})
	abs := filepath.Join(other, "shared.yaml")
	_, f, err := parseIn(t, map[string]string{"main.yaml": hdr + "- runFlow: " + abs + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Commands[0].SubFile != abs {
		t.Errorf("%q", f.Commands[0].SubFile)
	}
}

// TestRunFlowSameFileTwice pins that one sub-flow may be run many times (not a cycle).
func TestRunFlowSameFileTwice(t *testing.T) {
	_, f, err := parseIn(t, map[string]string{
		"main.yaml":   hdr + "- runFlow: a.yaml\n- runFlow: b.yaml\n",
		"a.yaml":      hdr + "- runFlow: common.yaml\n",
		"b.yaml":      hdr + "- runFlow: common.yaml\n- runFlow: common.yaml\n",
		"common.yaml": hdr + "- back\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Commands[1].SubFlow.Commands) != 2 {
		t.Errorf("%+v", f.Commands[1].SubFlow.Commands)
	}
}

// TestRunFlowInlineCommands pins inline `commands`, nesting and their env.
func TestRunFlowInlineCommands(t *testing.T) {
	c := step(t, `- runFlow:
    env: {A: "1"}
    commands:
      - tapOn: X
      - back
      - runFlow:
          commands:
            - repeat:
                times: 2
                commands: [scroll]
`)
	if c.SubFile != "" || c.SubFlow != nil || !reflect.DeepEqual(names(c.Commands), []string{"tapOn", "back", "runFlow"}) {
		t.Fatalf("%+v", c)
	}
	if !reflect.DeepEqual(c.Env, []KV{{"A", "1"}}) {
		t.Errorf("env: %v", c.Env)
	}
	rep := c.Commands[2].Commands[0]
	if rep.Name != "repeat" || rep.Times != "2" || rep.Commands[0].Name != "scroll" || rep.Line != 10 {
		t.Errorf("nested repeat: %+v", rep)
	}
}

// TestRunFlowErrors pins runFlow's rejections and where they are reported.
func TestRunFlowErrors(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.yaml":     hdr + "- back\n", // exists, so running itself is detected
		"sub.yaml":      hdr + "- back\n",
		"bad.yaml":      hdr + "- back\n- tapOnn: X\n",
		"noconfig.yaml": "- back\n",
		"folder/x.yaml": hdr + "- back\n",
		"a.yaml":        hdr + "- runFlow: b.yaml\n",
		"b.yaml":        hdr + "- runFlow: a.yaml\n",
		"self.yaml":     hdr + "- runFlow: self.yaml\n",
		"deep.yaml":     hdr + "- runFlow: a.yaml\n",
	})
	main := filepath.Join(dir, "main.yaml")
	for _, tc := range []struct {
		name, body, want, file string
		line                   int
	}{
		{"file and commands", "- runFlow: {file: sub.yaml, commands: [back]}\n", "runFlow needs exactly one of file or commands", "main.yaml", 3},
		{"neither", "- runFlow: {label: x}\n", "runFlow needs exactly one of file or commands", "main.yaml", 3},
		{"commands not a list", "- runFlow: {commands: back}\n", "commands must be a list", "main.yaml", 3},
		{"missing file", "- runFlow: nope.yaml\n", "flow file not found: " + filepath.Join(dir, "nope.yaml"), "main.yaml", 3},
		{"missing file in map", "- runFlow:\n    file: nope.yaml\n", "flow file not found", "main.yaml", 4},
		{"directory", "- runFlow: folder\n", "is a directory, not a flow file", "main.yaml", 3},
		{"self", "- runFlow: main.yaml\n", "a flow cannot run itself", "main.yaml", 3},
		{"bad env", "- runFlow: {file: sub.yaml, env: [A]}\n", "env must be a map", "main.yaml", 3},
		{"env without value", "- runFlow:\n    file: sub.yaml\n    env:\n      A:\n", "Environment variable A has no value", "main.yaml", 6},
		{"bad when", "- runFlow: {file: sub.yaml, when: {platform: tv}}\n", "platform must be", "main.yaml", 3},
		{"unknown key", "- runFlow: {file: sub.yaml, args: x}\n", `"args" is not an option of runFlow`, "main.yaml", 3},
		{"bad optional", "- runFlow: {file: sub.yaml, optional: x}\n", "expected true or false", "main.yaml", 3},
		{"bad inline command", "- runFlow:\n    commands:\n      - frob: x\n", "Invalid Command", "main.yaml", 5},
		{"error inside the sub-flow", "- runFlow: bad.yaml\n", "Did you mean tapOn", "bad.yaml", 4},
		{"sub-flow needs a config", "- runFlow: noconfig.yaml\n", "Config Section Required", "noconfig.yaml", 1},
		{"sub-flow runs itself", "- runFlow: self.yaml\n", "a flow cannot run itself", "self.yaml", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes(main, []byte(hdr+tc.body))
			e := asFlowErr(t, err)
			if !strings.Contains(e.Msg, tc.want) || e.Line != tc.line || e.File != filepath.Join(dir, tc.file) {
				t.Errorf("got %v; want %s:%d %q", e, tc.file, tc.line, tc.want)
			}
		})
	}

	t.Run("cycle", func(t *testing.T) {
		_, err := ParseFile(filepath.Join(dir, "deep.yaml"))
		e := asFlowErr(t, err)
		chain := strings.Join([]string{filepath.Join(dir, "deep.yaml"), filepath.Join(dir, "a.yaml"),
			filepath.Join(dir, "b.yaml"), filepath.Join(dir, "a.yaml")}, " -> ")
		if !strings.Contains(e.Msg, "runFlow cycle: "+chain) || e.File != filepath.Join(dir, "a.yaml") {
			t.Errorf("%v", e)
		}
	})
}

// TestRunFlowListValue pins that a list is not a valid runFlow value.
func TestRunFlowListValue(t *testing.T) {
	parseErr(t, hdr+"- runFlow: [a.yaml]\n")
}

// ---- runScript and evalScript ----

// TestRunScript pins runScript's scalar and map forms; the script source is read at parse time.
func TestRunScript(t *testing.T) {
	dir := writeFiles(t, map[string]string{"scripts/s.js": "output.ok = true\n"})
	main := filepath.Join(dir, "main.yaml")
	f, err := ParseBytes(main, []byte(hdr+`- runScript: scripts/s.js
- runScript:
    file: scripts/s.js
    env:
      N: "2"
    when:
      true: ${output.go}
    label: run it
    optional: true
`))
	if err != nil {
		t.Fatal(err)
	}
	js := filepath.Join(dir, "scripts", "s.js")
	if c := f.Commands[0]; c.SubFile != js || c.Script != "output.ok = true\n" || c.Env != nil {
		t.Errorf("scalar: %+v", c)
	}
	c := f.Commands[1]
	if c.SubFile != js || c.Script == "" || !reflect.DeepEqual(c.Env, []KV{{"N", "2"}}) || *c.When.True != "${output.go}" ||
		c.Label != "run it" || !c.Optional {
		t.Errorf("map: %+v", c)
	}
	for _, tc := range []struct {
		name, body, want string
		line             int
	}{
		{"missing script", "- runScript: nope.js\n", "script file not found: " + filepath.Join(dir, "nope.js"), 3},
		{"missing script in map", "- runScript:\n    file: nope.js\n", "script file not found", 4},
		{"map without file", "- runScript: {env: {A: b}}\n", "runScript needs file", 3},
		{"bad env", "- runScript: {file: scripts/s.js, env: x}\n", "env must be a map", 3},
		{"bad when", "- runScript: {file: scripts/s.js, when: x}\n", "a condition is a map", 3},
		{"unknown key", "- runScript: {file: scripts/s.js, args: x}\n", `"args" is not an option of runScript`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes(main, []byte(hdr+tc.body))
			if e := asFlowErr(t, err); !strings.Contains(e.Msg, tc.want) || e.Line != tc.line {
				t.Errorf("%v", e)
			}
		})
	}
}

// TestRunScriptListValue pins that a list runScript value is an error, not a crash.
func TestRunScriptListValue(t *testing.T) {
	parseErr(t, hdr+"- runScript: [s.js]\n")
}

// ---- repeat and retry ----

// TestRepeat pins repeat's times/while/commands.
func TestRepeat(t *testing.T) {
	c := step(t, "- repeat:\n    times: 3\n    commands:\n      - tapOn: Next\n      - back\n")
	if c.Times != "3" || c.While != nil || !reflect.DeepEqual(names(c.Commands), []string{"tapOn", "back"}) {
		t.Errorf("times: %+v", c)
	}
	c = step(t, "- repeat:\n    times: ${COUNT}\n    while:\n      visible: More\n    label: page\n    optional: true\n    commands: [{tapOn: More}]\n")
	if c.Times != "${COUNT}" || c.While.Visible.Text != "More" || c.Label != "page" || !c.Optional {
		t.Errorf("times + while: %+v", c)
	}
	c = step(t, "- repeat:\n    while: {notVisible: Done}\n    commands: [scroll]\n")
	if c.Times != "" || c.While.NotVisible.Text != "Done" {
		t.Errorf("while only: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- repeat: 3\n", "repeat takes a map with commands", 3},
		{"no commands", "- repeat: {times: 3}\n", "repeat needs commands", 3},
		{"commands not a list", "- repeat: {times: 3, commands: back}\n", "repeat needs commands", 3},
		{"unknown key", "- repeat: {times: 3, count: 2, commands: [back]}\n", `"count" is not an option of repeat`, 3},
		{"bad while", "- repeat: {while: {platform: tv}, commands: [back]}\n", "platform must be", 3},
		{"bad inner command", "- repeat:\n    times: 2\n    commands:\n      - tapOn: {txt: X}\n", `"txt" is not an option of tapOn`, 6},
		// issue #41: a literal times that is not a whole number of 0 or more is caught by tb check.
		{"times not a number", "- repeat:\n    times: abc\n    commands: [back]\n", `invalid times "abc"`, 4},
		{"times empty", "- repeat:\n    times: ''\n    commands: [back]\n", `invalid times ""`, 4},
		{"times negative", "- repeat: {times: -2, commands: [back]}\n", `invalid times "-2"`, 3},
		{"times NaN", "- repeat: {times: .nan, commands: [back]}\n", `invalid times ".nan"`, 3},
		{"times Inf", "- repeat: {times: Inf, commands: [back]}\n", `invalid times "Inf"`, 3},
	})
}

// TestRetry pins retry's commands and file forms.
func TestRetry(t *testing.T) {
	dir := writeFiles(t, map[string]string{"flaky.yaml": hdr + "- tapOn: Flaky\n"})
	main := filepath.Join(dir, "main.yaml")
	f, err := ParseBytes(main, []byte(hdr+`- retry:
    maxRetries: 3
    label: try hard
    optional: true
    commands:
      - tapOn: X
- retry:
    file: flaky.yaml
    maxRetries: ${N}
    env:
      MODE: fast
`))
	if err != nil {
		t.Fatal(err)
	}
	c := f.Commands[0]
	if c.MaxRetries != "3" || c.Label != "try hard" || !c.Optional || len(c.Commands) != 1 || c.SubFile != "" {
		t.Errorf("commands form: %+v", c)
	}
	c = f.Commands[1]
	if c.SubFile != filepath.Join(dir, "flaky.yaml") || c.SubFlow == nil || c.MaxRetries != "${N}" ||
		!reflect.DeepEqual(c.Env, []KV{{"MODE", "fast"}}) {
		t.Errorf("file form: %+v", c)
	}
	if c := step(t, "- retry: {commands: [back]}\n"); c.MaxRetries != "" {
		t.Errorf("maxRetries defaults to empty (the engine picks): %q", c.MaxRetries)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"scalar", "- retry: 3\n", "retry takes a map with commands or file"},
		{"both", "- retry: {file: flaky.yaml, commands: [back]}\n", "retry needs exactly one of file or commands"},
		{"neither", "- retry: {maxRetries: 2}\n", "retry needs exactly one of file or commands"},
		{"missing file", "- retry: {file: nope.yaml}\n", "flow file not found"},
		{"bad env", "- retry: {commands: [back], env: [x]}\n", "env must be a map"},
		{"unknown key", "- retry: {commands: [back], retries: 2}\n", `"retries" is not an option of retry`},
		{"bad inner command", "- retry: {commands: [frob]}\n", "Invalid Command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes(main, []byte(hdr+tc.body))
			if e := asFlowErr(t, err); !strings.Contains(e.Msg, tc.want) || e.Line != 3 {
				t.Errorf("%v", e)
			}
		})
	}
}

// TestRetryCommandsMustBeAList pins that retry's commands must be a list, as in runFlow and repeat.
func TestRetryCommandsMustBeAList(t *testing.T) {
	parseErr(t, hdr+"- retry: {commands: back}\n")
	parseErr(t, hdr+"- retry:\n    commands:\n")
}

// TestHooksAndNesting pins that onFlowStart/onFlowComplete accept the structured commands too.
func TestHooksAndNesting(t *testing.T) {
	_, f, err := parseIn(t, map[string]string{
		"main.yaml": `appId: com.example
onFlowStart:
  - runFlow: setup.yaml
  - retry:
      commands:
        - repeat: {times: 2, commands: [back]}
onFlowComplete:
  - runFlow:
      when: {platform: android}
      commands: [stopApp]
---
- back
`,
		"setup.yaml": hdr + "- launchApp: {clearState: true}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	st := f.Config.OnFlowStart
	if st[0].SubFlow == nil || !st[0].SubFlow.Commands[0].ClearState {
		t.Errorf("setup sub-flow: %+v", st[0])
	}
	if st[1].Commands[0].Name != "repeat" || st[1].Commands[0].Commands[0].Name != "back" {
		t.Errorf("nested: %+v", st[1])
	}
	if c := f.Config.OnFlowComplete[0]; c.When.Platform != "android" || c.Commands[0].Name != "stopApp" {
		t.Errorf("complete: %+v", c)
	}
}
