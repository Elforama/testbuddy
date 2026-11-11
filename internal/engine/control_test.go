package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// runner runs a whole flow on a new runner and returns both, so tests can look inside.
func runner(t *testing.T, d *fakeDriver, o Options, dir, src string) (*Runner, FlowResult) {
	t.Helper()
	fl, err := flow.ParseBytes(dir+"/flow.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	r := New(d, o)
	return r, r.RunFlow(context.Background(), fl)
}

// output reads a value the flow's scripts left in `output`.
func output(r *Runner, k string) string { return fmt.Sprint(r.jsEngine().Output()[k]) }

// onFlowStart runs before the commands and onFlowComplete after; only commands are steps.
func TestOnFlowStartAndComplete(t *testing.T) {
	hooks := "appId: app\nonFlowStart:\n  - inputText: start\nonFlowComplete:\n  - inputText: end\n---\n"
	f := &fakeDriver{screen: static()}
	res := runSrc(t, f, quick, t.TempDir(), hooks+"- inputText: main")
	mustPass(t, res)
	if want := []string{"start", "main", "end"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	if len(res.Steps) != 1 || res.Steps[0].Desc != "Input text main" || res.Name != "flow" || res.Duration <= 0 {
		t.Errorf("result %+v", res)
	}

	// A failing command: onFlowComplete still runs; the command's error is the flow's.
	f = &fakeDriver{screen: static()}
	res = runSrc(t, f, quick, t.TempDir(), hooks+"- assertTrue: \"false\"\n- inputText: skipped")
	mustFail(t, res, "Assertion is false")
	if want := []string{"start", "end"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}

	// A failing onFlowStart: no commands, onFlowComplete runs.
	f = &fakeDriver{screen: static()}
	res = runSrc(t, f, quick, t.TempDir(), "appId: app\nonFlowStart:\n  - assertTrue: \"false\"\nonFlowComplete:\n  - inputText: end\n---\n- inputText: main")
	mustFail(t, res, "Assertion is false")
	if want := []string{"end"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	if len(res.Steps) != 0 {
		t.Errorf("steps %+v", res.Steps)
	}

	// A failing onFlowComplete fails a flow that passed.
	f = &fakeDriver{screen: static()}
	res = runSrc(t, f, quick, t.TempDir(), "appId: app\nonFlowComplete:\n  - assertTrue: \"${1 > 2}\"\n---\n- inputText: main")
	mustFail(t, res, "Assertion is false")
	if res.Steps[0].Status != Passed {
		t.Errorf("steps %+v", res.Steps)
	}
}

// A cancelled run runs nothing, not even onFlowComplete.
func TestRunFlowCancelled(t *testing.T) {
	f := &fakeDriver{screen: static()}
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\nonFlowStart:\n  - inputText: start\nonFlowComplete:\n  - inputText: end\n---\n- inputText: main"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := New(f, quick).RunFlow(ctx, fl)
	if !errors.Is(res.Err, context.Canceled) || res.Status != Failed || len(f.typed) != 0 {
		t.Errorf("err %v status %s typed %q", res.Err, res.Status, f.typed)
	}
}

// -e values come first and the flow's env after: the flow's own values win, and
// ${X || 'default'} keeps a value passed in.
func TestEnvPrecedence(t *testing.T) {
	src := "appId: app\nenv:\n  X: ${X || 'dx'}\n  Y: flow\n  Z: ${Z || 'dz'}\n---\n- inputText: ${X}/${Y}/${Z}"
	f := &fakeDriver{screen: static()}
	o := quick
	o.Env = []flow.KV{{Key: "X", Value: "cli"}, {Key: "Y", Value: "cli"}}
	mustPass(t, runSrc(t, f, o, t.TempDir(), src))
	if want := []string{"cli/flow/dz"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	f = &fakeDriver{screen: static()}
	mustPass(t, runSrc(t, f, quick, t.TempDir(), src))
	if want := []string{"dx/flow/dz"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("without -e: typed %q, want %q", f.typed, want)
	}
}

// One env block's values are all evaluated before any is set: siblings don't see each other.
func TestEnvSiblings(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runSrc(t, f, quick, t.TempDir(), "appId: app\nenv:\n  A: one\n  B: ${A}\n---\n- inputText: ${A}-${B}"))
	if want := []string{"one-undefined"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	// -e values are evaluated too, and the flow's env sees them.
	f = &fakeDriver{screen: static()}
	o := quick
	o.Env = []flow.KV{{Key: "N", Value: "${2*3}"}}
	mustPass(t, runSrc(t, f, o, t.TempDir(), "appId: app\nenv:\n  M: ${N}!\n---\n- inputText: ${M}"))
	if want := []string{"6!"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
}

// A script error in -e values, the flow's env or its appId fails the flow before any command.
func TestEnvErrors(t *testing.T) {
	o := quick
	o.Env = []flow.KV{{Key: "X", Value: "${(}"}}
	f := &fakeDriver{screen: static()}
	mustFail(t, runSrc(t, f, o, t.TempDir(), "appId: app\n---\n- inputText: x"), "JavaScript error")
	for _, src := range []string{
		"appId: app\nenv:\n  X: ${(}\n---\n- inputText: x",
		"appId: ${(}\n---\n- inputText: x",
	} {
		f := &fakeDriver{screen: static()}
		mustFail(t, runSrc(t, f, quick, t.TempDir(), src), "JavaScript error")
		if len(f.typed) != 0 {
			t.Errorf("%q: ran commands", src)
		}
	}
}

// The appId is evaluated and is the default for app commands.
func TestAppIDEvaluated(t *testing.T) {
	f := &fakeDriver{screen: static()}
	o := quick
	o.Env = []flow.KV{{Key: "APP", Value: "com.example"}}
	mustPass(t, runSrc(t, f, o, t.TempDir(), "appId: ${APP}.debug\n---\n- launchApp\n- stopApp"))
	if got := f.called("LaunchApp"); !reflect.DeepEqual(got, []string{"LaunchApp com.example.debug"}) {
		t.Errorf("calls %q", got)
	}
}

// runFlow commands: a nested scope for env; the parent's values come back after.
func TestRunFlowCommandsEnv(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runSrc(t, f, quick, t.TempDir(), `appId: app
env:
  X: outer
---
- runFlow:
    env:
      X: inner
      Y: ${X}
    commands:
      - inputText: ${X}-${Y}
- inputText: ${X}-${Y}
`))
	if want := []string{"inner-outer", "outer-undefined"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
}

// runFlow of a file: the command's env, then the sub-flow's own env (which wins), its
// onFlowStart and onFlowComplete; nothing leaks back.
func TestRunFlowFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub.yaml", `appId: app
env:
  S: sub
  P: ${P || 'pd'}
onFlowStart:
  - inputText: sub-start
onFlowComplete:
  - inputText: sub-end
---
- inputText: ${S}-${P}
`)
	f := &fakeDriver{screen: static()}
	res := runSrc(t, f, quick, dir, "appId: app\n---\n- runFlow: {file: sub.yaml, env: {P: param, S: fromcmd}}\n- runFlow: sub.yaml\n- inputText: after-${S}")
	mustPass(t, res)
	want := []string{"sub-start", "sub-param", "sub-end", "sub-start", "sub-pd", "sub-end", "after-undefined"}
	if !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	if res.Steps[0].Desc != "Run sub" {
		t.Errorf("desc %q", res.Steps[0].Desc)
	}
}

// A sub-flow's failure fails runFlow; its onFlowComplete still runs.
func TestRunFlowFileFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub.yaml", "appId: app\nonFlowComplete:\n  - inputText: cleanup\n---\n- assertTrue: \"false\"\n- inputText: never")
	writeFile(t, dir, "badenv.yaml", "appId: app\nenv:\n  X: ${(}\n---\n- inputText: never")
	writeFile(t, dir, "badstart.yaml", "appId: app\nonFlowStart:\n  - assertTrue: \"false\"\n---\n- inputText: never")
	writeFile(t, dir, "badend.yaml", "appId: app\nonFlowComplete:\n  - assertTrue: \"false\"\n---\n- inputText: body")
	f := &fakeDriver{screen: static()}
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- runFlow: sub.yaml\n- inputText: after"), "Assertion is false")
	if want := []string{"cleanup"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	mustFail(t, runSrc(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- runFlow: badenv.yaml"), "JavaScript error")
	f = &fakeDriver{screen: static()}
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- runFlow: badstart.yaml"), "Assertion is false")
	if len(f.typed) != 0 {
		t.Errorf("badstart typed %q", f.typed)
	}
	f = &fakeDriver{screen: static()}
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- runFlow: badend.yaml"), "Assertion is false")
	if want := []string{"body"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("badend typed %q", f.typed)
	}
	mustFail(t, runSrc(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- runFlow: {file: sub.yaml, env: {X: '${(}'}}"), "JavaScript error")
}

// runFlow's when: visible, notVisible, true and platform, alone or together; a false one skips.
func TestRunFlowWhen(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		when string
		runs bool
	}{
		{"{visible: Home}", true},
		{"{visible: Nope}", false},
		{"{notVisible: Nope}", true},
		{"{notVisible: Home}", false},
		{"{true: \"${1 < 2}\"}", true},
		{"{true: \"${1 > 2}\"}", false},
		{"{true: \"\"}", false},
		{"{platform: Android}", true},
		{"{platform: iOS}", false},
		{"{platform: android, visible: Home, notVisible: Nope, true: 'yes'}", true},
		{"{platform: android, visible: Nope}", false},
		{"{visible: Home, true: 'false'}", false},
		{"{visible: Nope, timeout: 50}", false},
		{"{visible: Home, timeout: \"${'1_000'}\"}", true},
	} {
		t.Run(tc.when, func(t *testing.T) {
			f := &fakeDriver{screen: static(btn("Home", "", 0))}
			res := runWith(t, f, quick, "- runFlow:\n    when: "+tc.when+"\n    commands:\n      - inputText: ran")
			mustPass(t, res)
			if ran := len(f.typed) == 1; ran != tc.runs {
				t.Errorf("ran %v, want %v", ran, tc.runs)
			}
			want := Passed
			if !tc.runs {
				want = Skipped
			}
			if res.Steps[0].Status != want {
				t.Errorf("status %s, want %s", res.Steps[0].Status, want)
			}
		})
	}
}

// A when condition that cannot be evaluated fails runFlow.
func TestRunFlowWhenErrors(t *testing.T) {
	for _, tc := range []struct{ when, want string }{
		{"{true: \"${(}\"}", "JavaScript error"},
		{"{visible: '${(}'}", "JavaScript error"},
		{"{notVisible: '${(}'}", "JavaScript error"},
		{"{visible: Home, timeout: soon}", `invalid timeout "soon"`},
		{"{visible: Home, timeout: '${(}'}", "JavaScript error"},
		{"{visible: {text: Home, index: x}}", `invalid index "x"`},
	} {
		f := &fakeDriver{screen: static(btn("Home", "", 0))}
		mustFail(t, runWith(t, f, quick, "- runFlow:\n    when: "+tc.when+"\n    commands:\n      - inputText: ran"), tc.want)
		if len(f.typed) != 0 {
			t.Errorf("%s: ran", tc.when)
		}
	}
}

// A runFlow skipped by its condition does not count as an interaction for lookup budgets.
func TestSkippedRunFlowIsNoInteraction(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	start := time.Now()
	r, res := runner(t, f, quick, t.TempDir(), "appId: app\n---\n- runFlow:\n    when: {visible: Nope, timeout: 150}\n    commands:\n      - inputText: x")
	mustPass(t, res)
	if d := r.lastInteraction.Sub(start); d > 100*time.Millisecond {
		t.Errorf("last interaction %s after the start: the skipped runFlow counted", d)
	}
	r, res = runner(t, f, quick, t.TempDir(), "appId: app\n---\n- runFlow:\n    when: {notVisible: Nope}\n    commands:\n      - evalScript: ${1}")
	mustPass(t, res)
	if time.Since(r.lastInteraction) > 50*time.Millisecond {
		t.Errorf("a runFlow that ran did not count as an interaction")
	}
}

// repeat runs its commands a number of times (evaluated); zero times is a skip.
func TestRepeatTimes(t *testing.T) {
	for _, tc := range []struct {
		times string
		n     int
	}{{"3", 3}, {"\"${1+1}\"", 2}, {"2.9", 2}, {"1", 1}} {
		f := &fakeDriver{screen: static()}
		mustPass(t, runWith(t, f, quick, "- repeat:\n    times: "+tc.times+"\n    commands:\n      - inputText: x"))
		if len(f.typed) != tc.n {
			t.Errorf("times %s: %d runs, want %d", tc.times, len(f.typed), tc.n)
		}
	}
	f := &fakeDriver{screen: static()}
	res := runWith(t, f, quick, "- repeat:\n    times: 0\n    commands:\n      - inputText: x")
	mustPass(t, res)
	if res.Steps[0].Status != Skipped || len(f.typed) != 0 {
		t.Errorf("times 0: %s, typed %q", res.Steps[0].Status, f.typed)
	}
}

// issue #41: a times that only becomes known at run time and is not a whole number of 0 or
// more fails the repeat at once, before its commands run, instead of looping without end.
func TestRepeatBadTimesFails(t *testing.T) {
	for _, tc := range []struct{ times, want string }{
		{`"${'abc'}"`, `invalid times "abc" (from "${'abc'}")`},
		{"${COUNT}", `invalid times "undefined" (from "${COUNT}")`},
		{`"${''}"`, `invalid times "" (from "${''}")`},
		{`"${-1}"`, `invalid times "-1" (from "${-1}")`},
		{`"${1/0}"`, `invalid times "Infinity" (from "${1/0}")`},
		{`"${0/0}"`, `invalid times "NaN" (from "${0/0}")`},
	} {
		t.Run(tc.times, func(t *testing.T) {
			f := &fakeDriver{screen: static()}
			// The loop must end on its own: a short deadline turns a hang into a clear failure.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			fl, err := flow.ParseBytes(t.TempDir()+"/flow.yaml", []byte("appId: app\n---\n- repeat:\n    times: "+tc.times+"\n    commands:\n      - inputText: x"))
			if err != nil {
				t.Fatal(err)
			}
			res := New(f, quick).RunFlow(ctx, fl)
			if ctx.Err() != nil {
				t.Fatalf("repeat did not end: still looping at the 2s deadline after %d runs (status %s, err %v)", len(f.typed), res.Status, res.Err)
			}
			mustFail(t, res, tc.want)
			if len(f.typed) != 0 {
				t.Errorf("the commands ran %d times before the failure", len(f.typed))
			}
		})
	}
}

// repeat while: the condition is checked before each round; times caps the rounds.
func TestRepeatWhile(t *testing.T) {
	f := &fakeDriver{screen: static()}
	r, res := runner(t, f, quick, t.TempDir(), "appId: app\n---\n- repeat:\n    while: {true: \"${(output.n || 0) < 3}\"}\n    commands:\n      - evalScript: ${output.n = (output.n || 0) + 1}")
	mustPass(t, res)
	if got := output(r, "n"); got != "3" {
		t.Errorf("output.n = %s, want 3", got)
	}
	// Both: whichever ends first.
	f = &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- repeat:\n    times: 2\n    while: {true: 'yes'}\n    commands:\n      - inputText: x"))
	if len(f.typed) != 2 {
		t.Errorf("times 2 while true: %d runs", len(f.typed))
	}
	// False from the start: skipped.
	f = &fakeDriver{screen: static()}
	res = runWith(t, f, quick, "- repeat:\n    while: {true: 'false'}\n    commands:\n      - inputText: x")
	if res.Steps[0].Status != Skipped || len(f.typed) != 0 {
		t.Errorf("while false: %s", res.Steps[0].Status)
	}
}

// repeat while visible: taps until the element goes.
func TestRepeatWhileVisible(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var left atomic.Int64
	left.Store(3)
	f.screen = func() []*ui.Node {
		if left.Load() > 0 {
			return []*ui.Node{btn("Next", "next", 100)}
		}
		return []*ui.Node{btn("Done", "", 100)}
	}
	f.onTap = func(x, y int) { left.Add(-1); f.change(true) }
	mustPass(t, runWith(t, f, quick, "- repeat:\n    while: {visible: Next}\n    commands:\n      - tapOn: Next\n- assertVisible: Done"))
	if len(f.taps) != 3 {
		t.Errorf("%d taps, want 3", len(f.taps))
	}
}

// A failure inside repeat stops it and fails it; bad times or while values fail it.
func TestRepeatErrors(t *testing.T) {
	f := &fakeDriver{screen: static()}
	r, res := runner(t, f, quick, t.TempDir(), "appId: app\n---\n- repeat:\n    times: 5\n    commands:\n      - evalScript: ${output.n = (output.n || 0) + 1}\n      - assertTrue: ${output.n < 2}")
	mustFail(t, res, "Assertion is false")
	if got := output(r, "n"); got != "2" {
		t.Errorf("ran %s rounds, want 2", got)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- repeat:\n    times: '${(}'\n    commands:\n      - inputText: x"), "JavaScript error")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- repeat:\n    while: {true: '${(}'}\n    commands:\n      - inputText: x"), "JavaScript error")
}

// Each repeat round gets a fresh env scope.
func TestRepeatEnvScope(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- repeat:\n    times: 2\n    commands:\n      - runFlow:\n          env: {X: set}\n          commands:\n            - inputText: ${X}\n      - inputText: ${X}"))
	if want := []string{"set", "undefined", "set", "undefined"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
}

// retry runs its commands again after a test failure, up to maxRetries more times (at most 3).
func TestRetry(t *testing.T) {
	for _, tc := range []struct {
		max      string
		passOn   int // the attempt that passes (0: never)
		attempts string
		pass     bool
	}{
		{"", 2, "2", true},
		{"", 0, "2", false},
		{"maxRetries: 2", 3, "3", true},
		{"maxRetries: 10", 0, "4", false},
		{"maxRetries: \"${0}\"", 0, "1", false},
		{"maxRetries: lots", 0, "2", false}, // unreadable: the default
	} {
		t.Run(tc.max, func(t *testing.T) {
			f := &fakeDriver{screen: static()}
			cond := "false"
			if tc.passOn > 0 {
				cond = fmt.Sprintf("${output.n >= %d}", tc.passOn)
			}
			src := "appId: app\n---\n- retry:\n    " + tc.max + "\n    commands:\n      - evalScript: ${output.n = (output.n || 0) + 1}\n      - assertTrue: \"" + cond + "\""
			r, res := runner(t, f, quick, t.TempDir(), src)
			if tc.pass {
				mustPass(t, res)
			} else {
				mustFail(t, res, "Assertion is false")
			}
			if got := output(r, "n"); got != tc.attempts {
				t.Errorf("%s attempts, want %s", got, tc.attempts)
			}
		})
	}
}

// A script error is no test failure: retry does not run it again.
func TestRetryScriptErrorNotRetried(t *testing.T) {
	f := &fakeDriver{screen: static()}
	r, res := runner(t, f, quick, t.TempDir(), "appId: app\n---\n- retry:\n    maxRetries: 3\n    commands:\n      - evalScript: ${output.n = (output.n || 0) + 1}\n      - evalScript: ${nope()}")
	mustFail(t, res, "JavaScript error")
	if got := output(r, "n"); got != "1" {
		t.Errorf("%s attempts, want 1", got)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- retry:\n    maxRetries: '${(}'\n    commands:\n      - inputText: x"), "JavaScript error")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- retry:\n    env: {X: '${(}'}\n    commands:\n      - inputText: x"), "JavaScript error")
}

// retry of a file runs the sub-flow's hooks each attempt, with the retry's env.
func TestRetryFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "flaky.yaml", "appId: app\nenv:\n  S: ${S || 'sd'}\nonFlowStart:\n  - inputText: start-${S}\nonFlowComplete:\n  - inputText: end\n---\n- evalScript: ${output.n = (output.n || 0) + 1}\n- assertTrue: ${output.n >= 2}")
	writeFile(t, dir, "badenv.yaml", "appId: app\nenv:\n  X: ${(}\n---\n- inputText: x")
	writeFile(t, dir, "badstart.yaml", "appId: app\nonFlowStart:\n  - assertTrue: 'false'\n---\n- inputText: x")
	writeFile(t, dir, "badend.yaml", "appId: app\nonFlowComplete:\n  - assertTrue: 'false'\n---\n- inputText: body")
	f := &fakeDriver{screen: static()}
	r, res := runner(t, f, quick, dir, "appId: app\n---\n- retry:\n    env: {S: given}\n    file: flaky.yaml")
	mustPass(t, res)
	if want := []string{"start-given", "end", "start-given", "end"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	if got := output(r, "n"); got != "2" {
		t.Errorf("%s attempts", got)
	}
	mustFail(t, runSrc(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- retry: {file: badenv.yaml}"), "JavaScript error")
	f = &fakeDriver{screen: static()}
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- retry: {file: badstart.yaml}"), "Assertion is false")
	if len(f.typed) != 0 {
		t.Errorf("badstart typed %q", f.typed)
	}
	f = &fakeDriver{screen: static()}
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- retry: {file: badend.yaml}"), "Assertion is false")
	if want := []string{"body", "body"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("badend typed %q", f.typed)
	}
}

// evalScript runs JavaScript for its effects; a script error fails it.
func TestEvalScript(t *testing.T) {
	f := &fakeDriver{screen: static()}
	r, res := runner(t, f, quick, t.TempDir(), "appId: app\n---\n- evalScript: ${output.x = 'y'}\n- evalScript: {script: \"${output.z = output.x + '!'}\"}\n- inputText: ${output.z}")
	mustPass(t, res)
	if output(r, "z") != "y!" || !reflect.DeepEqual(f.typed, []string{"y!"}) {
		t.Errorf("output %v typed %q", r.jsEngine().Output(), f.typed)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- evalScript: ${throw new Error('boom')}"), "boom")
}

// runScript runs a script file with the env, its own env, and console.log reported.
func TestRunScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.js", "output.greeting = GREETING + ' ' + NAME; console.log('ran for', NAME, {a: 1});")
	writeFile(t, dir, "boom.js", "throw new Error('script broke')")
	rec := &recorder{}
	o := quick
	o.Reporter = rec
	f := &fakeDriver{screen: static()}
	r, res := runner(t, f, o, dir, "appId: app\nenv:\n  GREETING: hi\n---\n- runScript: {file: s.js, env: {NAME: bob}}\n- inputText: ${NAME}")
	mustPass(t, res)
	if output(r, "greeting") != "hi bob" {
		t.Errorf("output %v", r.jsEngine().Output())
	}
	if !reflect.DeepEqual(f.typed, []string{"undefined"}) {
		t.Errorf("the script's env leaked: typed %q", f.typed)
	}
	if logs := rec.logs(); len(logs) != 1 || logs[0] != "ran for bob {a: 1}" {
		t.Errorf("logs %q", logs)
	}
	if res.Steps[0].Desc != "Run s.js" {
		t.Errorf("desc %q", res.Steps[0].Desc)
	}
	// when: skipped on another platform.
	r, res = runner(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- runScript: {file: s.js, when: {platform: iOS}}")
	mustPass(t, res)
	if res.Steps[0].Status != Skipped || len(r.jsEngine().Output()) != 0 {
		t.Errorf("status %s output %v", res.Steps[0].Status, r.jsEngine().Output())
	}
	mustFail(t, runSrc(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- runScript: boom.js"), "script broke")
	mustFail(t, runSrc(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- runScript: {file: s.js, env: {NAME: '${(}'}}"), "JavaScript error")
	mustFail(t, runSrc(t, &fakeDriver{screen: static()}, quick, dir, "appId: app\n---\n- runScript: {file: s.js, when: {true: '${(}'}}"), "JavaScript error")
}

// Script errors are fatal: an optional command with a broken ${...} fails, it does not warn.
func TestJSErrorsAreFatal(t *testing.T) {
	for _, cmd := range []string{
		`- tapOn: {text: "${nope(}", optional: true}`,
		`- assertVisible: {text: "${(}", optional: true}`,
		`- assertNotVisible: {text: "${(}", optional: true}`,
		`- copyTextFrom: {text: "${(}", optional: true}`,
		`- scrollUntilVisible: {element: "${(}", optional: true}`,
		`- swipe: {direction: UP, from: {text: "${(}"}, optional: true}`,
		`- tapOn: {point: "${(}", optional: true}`,
		`- inputText: "${undefinedFunction()}"`,
		`- setClipboard: "${(}"`,
		`- openLink: "${(}"`,
		`- takeScreenshot: "${(}"`,
		`- startRecording: "${(}"`,
		`- assertScreenshot: "${(}"`,
	} {
		res := runWith(t, &fakeDriver{screen: static()}, quick, cmd)
		if res.Status != Failed || len(res.Steps) != 1 || res.Steps[0].Status != Failed || !strings.Contains(res.Steps[0].Err, "JavaScript error") {
			t.Errorf("%s: %s %+v", cmd, res.Status, res.Steps)
		}
	}
}

// Selector-level optional makes the command optional too.
func TestSelectorOptional(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	res := runWith(t, f, quick, "- scrollUntilVisible: {element: {text: Nope, optional: true}, timeout: 50}\n- assertVisible: {text: Nope, optional: true}\n- tapOn: {text: Nope, optional: true}")
	mustPass(t, res)
	if got := stepStatuses(res); !reflect.DeepEqual(got, []Status{Warned, Warned, Warned}) {
		t.Errorf("statuses %v", got)
	}
	// A non-test failure (the device erred) is not downgraded.
	f2 := &fakeDriver{screen: static(btn("Go", "go", 0))}
	f2.fail("Tap", errors.New("agent crashed"))
	mustFail(t, runWith(t, f2, quick, "- tapOn: {id: go, optional: true}"), "agent crashed")
}

// selOptional looks at the selector and at swipe's from.
func TestSelOptional(t *testing.T) {
	for _, tc := range []struct {
		c    *flow.Command
		want bool
	}{
		{&flow.Command{}, false},
		{&flow.Command{Selector: &flow.Selector{}}, false},
		{&flow.Command{Selector: &flow.Selector{Optional: true}}, true},
		{&flow.Command{From: &flow.Selector{Optional: true}}, true},
		{&flow.Command{From: &flow.Selector{}}, false},
	} {
		if got := selOptional(tc.c); got != tc.want {
			t.Errorf("%+v: %v", tc.c, got)
		}
	}
}

// dirOf is a script's folder, "." for a bare name.
func TestDirOf(t *testing.T) {
	for in, want := range map[string]string{"/a/b/c.js": "/a/b", "c.js": ".", "a/c.js": "a"} {
		if got := dirOf(in); got != want {
			t.Errorf("dirOf(%q) = %q, want %q", in, got, want)
		}
	}
}
