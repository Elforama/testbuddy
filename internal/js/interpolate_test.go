package js

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dop251/goja"

	"github.com/elforama/testbuddy/internal/legacy"
)

func scope(env map[string]string) *Scope { return &Scope{Env: env, Platform: "android"} }

// Interpolate follows the format's ${...} rules: expression bodies, escapes, literals, adjacency.
func TestInterpolateRules(t *testing.T) {
	e := New("android")
	env := scope(map[string]string{"A": "x", "B": "y", "N": "1", "EMPTY": ""})
	cases := []struct {
		name, in, want string
	}{
		{"no expression is returned as is", "plain text", "plain text"},
		{"a lone dollar is not an expression", "costs $5", "costs $5"},
		{"a dollar not followed by brace", "$A", "$A"},
		{"simple variable", "${A}", "x"},
		{"text around", "<${A}>", "<x>"},
		{"two expressions", "${A}-${B}", "x-y"},
		{"adjacent expressions", "${A}${B}${A}", "xyx"},
		{"repeated variable", "${A} ${A} ${A}", "x x x"},
		{"dollar before expression stays", "$${A}", "$x"},
		{"trailing dollar stays", "${A}$", "x$"},
		{"escaped expression is literal without the backslash", `\${A}`, "${A}"},
		{"escaped mid-text", `pre\${A}post`, "pre${A}post"},
		{"escaped then evaluated", `\${A} ${B}`, "${A} y"},
		{"evaluated then escaped", `${A} \${B}`, "x ${B}"},
		{"double backslash keeps one backslash", `\\${A}`, `\${A}`},
		{"unterminated expression kept literally", "a ${A", "a ${A"},
		{"unterminated at end", "${", "${"},
		{"close brace before open is text", "}${A}", "}x"},
		{"empty expression renders nothing", "a${}b", "ab"},
		{"blank expression renders nothing", "a${   }b", "ab"},
		{"expression with braces inside", "${ ({k: 'v'}).k }", "v"},
		{"string containing a close brace", "${'}'}", "}"},
		{"expression containing $ is not evaluated", "${'$'}", "${'$'}"},
		{"env values are strings (concatenation)", "${N + 1}", "11"},
		{"env values convert with Number", "${Number(N) + 1}", "2"},
		{"typeof env value", "${typeof N}", "string"},
		{"empty env value", "[${EMPTY}]", "[]"},
		{"undefined global reads as undefined", "${NOPE}", "undefined"},
		{"undefined global with default", "${NOPE || 'dflt'}", "dflt"},
		{"typeof undefined global", "${typeof NOPE}", "undefined"},
		{"defined global wins over default", "${A || 'dflt'}", "x"},
		{"null", "${null}", "null"},
		{"boolean true", "${1 < 2}", "true"},
		{"boolean false", "${1 > 2}", "false"},
		{"ternary", "${N == '1' ? 'one' : 'other'}", "one"},
		{"template-free string methods", "${A.toUpperCase()}", "X"},
		{"integer", "${40 + 2}", "42"},
		{"float", "${2.5}", "2.5"},
		{"float noise is kept", "${0.1 + 0.2}", "0.30000000000000004"},
		{"NaN", "${0/0}", "NaN"},
		{"Infinity", "${1/0}", "Infinity"},
		{"array", "${[1, 'a']}", `[1, "a"]`},
		{"object", "${({b: 1, a: 'x'})}", `{a: "x", b: 1}`},
		{"multi-line text", "line1\n${A}\nline3", "line1\nx\nline3"},
		{"unicode around", "é ${A} ✓", "é x ✓"},
		{"builtins survive", "${JSON.stringify({a: 1})}", `{"a":1}`},
		{"Math survives", "${Math.max(3, 9)}", "9"},
		{"parseInt survives", "${parseInt('42px')}", "42"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.Interpolate(c.in, env)
			if err != nil {
				t.Fatalf("Interpolate(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Interpolate(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The expression runs to the last "}" before the next "$" (greedy, as the format's rule says),
// so a stray "}" later in the text becomes part of the expression.
func TestInterpolateGreedyCloseBrace(t *testing.T) {
	e := New("android")
	_, err := e.Interpolate("${A} and }", scope(map[string]string{"A": "x"}))
	if err == nil {
		t.Fatal("expected a syntax error: the expression should run to the last }")
	}
	// A following ${ bounds the expression again.
	got, err := e.Interpolate("${A} } ${A}", scope(map[string]string{"A": "x"}))
	if err == nil {
		t.Fatalf("expected an error, got %q", got)
	}
}

// A JavaScript error stops interpolation and comes back as *Error.
func TestInterpolateErrors(t *testing.T) {
	e := New("android")
	for _, in := range []string{
		"${syntax error here}",
		"${undefinedFn()}",
		"${null.x}",
		"ok ${A} then ${(function(){ throw new Error('boom') })()}",
	} {
		t.Run(in, func(t *testing.T) {
			got, err := e.Interpolate(in, scope(map[string]string{"A": "x"}))
			if err == nil {
				t.Fatalf("Interpolate(%q) = %q, want an error", in, got)
			}
			var je *Error
			if !errors.As(err, &je) {
				t.Fatalf("error %T is not *js.Error", err)
			}
			if got != "" {
				t.Errorf("partial output %q returned with an error", got)
			}
			if !strings.HasPrefix(err.Error(), "JavaScript error: ") {
				t.Errorf("error text %q", err.Error())
			}
		})
	}
}

// Thrown values become the error message: Error objects as "Name: msg", others as their string.
func TestEvalThrownMessages(t *testing.T) {
	e := New("android")
	cases := map[string]string{
		"throw new Error('boom')":                      "Error: boom",
		"throw new TypeError('bad')":                   "TypeError: bad",
		"throw 'plain'":                                "plain",
		"throw 42":                                     "42",
		"undefinedFunction()":                          "TypeError",
		"(function(){ 'use strict'; undeclared.x })()": "TypeError",
	}
	for src, want := range cases {
		t.Run(src, func(t *testing.T) {
			_, err := e.Eval(src, nil)
			var je *Error
			if !errors.As(err, &je) {
				t.Fatalf("Eval(%q) err = %v, want *Error", src, err)
			}
			if !strings.Contains(je.Msg, want) {
				t.Errorf("Msg = %q, want it to contain %q", je.Msg, want)
			}
		})
	}
}

// Syntax errors are reported as JavaScript errors.
func TestEvalSyntaxError(t *testing.T) {
	e := New("android")
	_, err := e.Eval("var = ;", nil)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "SyntaxError") {
		t.Fatalf("err = %v, want a SyntaxError", err)
	}
}

// Error() prefixes the message so logs say where it came from.
func TestErrorString(t *testing.T) {
	if got := (&Error{Msg: "x"}).Error(); got != "JavaScript error: x" {
		t.Errorf("Error() = %q", got)
	}
}

// A Go panic raised during evaluation (here from the Log hook) is recovered as a JavaScript error.
func TestEvalRecoversGoPanic(t *testing.T) {
	e := New("android")
	e.Log = func(string) { panic("log hook exploded") }
	_, err := e.Eval("console.log('hi')", nil)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "log hook exploded") {
		t.Fatalf("err = %v, want the recovered panic", err)
	}
	// The engine still works afterwards.
	e.Log = nil
	if v, err := e.Eval("1 + 1", nil); err != nil || v.ToInteger() != 2 {
		t.Fatalf("after panic: %v, %v", v, err)
	}
}

// Eval returns the value of the last expression statement.
func TestEvalReturnsValue(t *testing.T) {
	e := New("android")
	v, err := e.Eval("var a = 2; a * 21", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 42 {
		t.Errorf("value = %v", v)
	}
	v, err = e.Eval("", nil)
	if err != nil || !goja.IsUndefined(v) {
		t.Errorf("empty source = %v, %v; want undefined", v, err)
	}
}

// Evaluation runs in strict mode: `this` is undefined and legacy octal literals are rejected.
func TestEvalStrictMode(t *testing.T) {
	e := New("android")
	if got, err := e.Interpolate("${typeof this}", nil); err != nil || got != "undefined" {
		t.Errorf("typeof this = %q, %v", got, err)
	}
	if _, err := e.Eval("010", nil); err == nil {
		t.Error("octal literal accepted outside strict mode")
	}
	if _, err := e.Eval("with ({a: 1}) { a }", nil); err == nil {
		t.Error("with statement accepted outside strict mode")
	}
}

// Globals a script creates (vars, functions, globalThis properties) do not survive to the next evaluation.
func TestGlobalsDoNotPersist(t *testing.T) {
	e := New("android")
	for _, src := range []string{"var v1 = 1", "function f1() { return 1 }", "globalThis.g1 = 3", "this === undefined"} {
		if _, err := e.Eval(src, nil); err != nil {
			t.Fatalf("Eval(%q): %v", src, err)
		}
	}
	for _, name := range []string{"v1", "f1", "g1"} {
		if got, _ := e.Interpolate("${typeof "+name+"}", nil); got != "undefined" {
			t.Errorf("%s leaked into the next evaluation: typeof = %q", name, got)
		}
	}
	// Within one evaluation a globalThis property is visible as a global.
	if v, err := e.Eval("globalThis.g2 = 7; g2 * 2", nil); err != nil || v.ToInteger() != 14 {
		t.Errorf("global within one eval: %v, %v", v, err)
	}
}

// Strict mode: reading an undeclared name gives undefined, but assigning to one is a ReferenceError.
func TestUndeclaredAssignmentThrows(t *testing.T) {
	e := New("android")
	_, err := e.Eval("implicit1 = 5", nil)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "ReferenceError") {
		t.Fatalf("err = %v, want a ReferenceError", err)
	}
	if got, _ := e.Interpolate("${typeof implicit1}", nil); got != "undefined" {
		t.Errorf("typeof implicit1 = %q", got)
	}
}

// Env variables are injected per evaluation and do not leak into the next one.
func TestEnvIsPerEvaluation(t *testing.T) {
	e := New("android")
	if got, _ := e.Interpolate("${USER}", scope(map[string]string{"USER": "ann"})); got != "ann" {
		t.Fatalf("USER = %q", got)
	}
	if got, _ := e.Interpolate("${typeof USER}", scope(map[string]string{"OTHER": "x"})); got != "undefined" {
		t.Errorf("USER leaked: %q", got)
	}
	if got, _ := e.Interpolate("${typeof USER}", nil); got != "undefined" {
		t.Errorf("USER leaked with nil scope: %q", got)
	}
	// A later scope may rebind the same name.
	if got, _ := e.Interpolate("${USER}", scope(map[string]string{"USER": "bob"})); got != "bob" {
		t.Errorf("rebinding USER = %q", got)
	}
}

// Env keys that are not identifiers are skipped, and env cannot replace permanent globals.
func TestEnvSkipsInvalidAndPermanentNames(t *testing.T) {
	e := New("android")
	env := map[string]string{
		"MY-VAR": "dash", "1ABC": "digit", "a b": "space", "": "empty",
		"output": "hijack", "tb": "hijack", legacy.ScriptObject(): "hijack", "json": "hijack",
		"http": "hijack", "faker": "hijack", "console": "hijack", "relativePoint": "hijack",
		"Math": "hijack", "JSON": "hijack", "__tb_src": "'hijacked'",
		"_ok": "u", "$ok": "d", "ok9": "n", "é": "accent",
	}
	sc := scope(env)
	cases := map[string]string{
		"${globalThis['MY-VAR']}":  "undefined",
		"${globalThis['1ABC']}":    "undefined",
		"${globalThis['a b']}":     "undefined",
		"${typeof output}":         "object",
		"${typeof tb}":             "object",
		"${typeof maestro}":        "object",
		"${typeof json}":           "function",
		"${typeof http}":           "object",
		"${typeof faker}":          "object",
		"${typeof console}":        "object",
		"${typeof relativePoint}":  "function",
		"${Math.abs(-1)}":          "1",
		"${JSON.stringify([1])}":   "[1]",
		"${'not hijacked'}":        "not hijacked",
		"${_ok}${ok9}${é}":         "unaccent",
		"${globalThis['\\x24ok']}": "d",
	}
	for in, want := range cases {
		if got, err := e.Interpolate(in, sc); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", in, got, err, want)
		}
	}
}

// validIdent accepts JavaScript-ish identifiers only.
func TestValidIdent(t *testing.T) {
	cases := map[string]bool{
		"": false, "a": true, "A1": true, "_": true, "$": true, "$x": true, "_9": true,
		"9a": false, "a-b": false, "a b": false, "a.b": false, "é": true, "名前": true, "a=b": false,
	}
	for s, want := range cases {
		if got := validIdent(s); got != want {
			t.Errorf("validIdent(%q) = %v, want %v", s, got, want)
		}
	}
}

// `output` persists across evaluations and scopes, and Output() exports it.
func TestOutputPersists(t *testing.T) {
	e := New("android")
	if got := e.Output(); len(got) != 0 {
		t.Fatalf("fresh output = %v", got)
	}
	if _, err := e.Eval("output.count = 1", scope(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Eval("output.count += 1; output.user = {name: 'ann', tags: ['a', 'b']}", scope(map[string]string{"X": "1"})); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Interpolate("${output.count}", nil); got != "2" {
		t.Errorf("output.count = %q", got)
	}
	if got, _ := e.Interpolate("${output.user.name}", &Scope{Platform: "ios"}); got != "ann" {
		t.Errorf("output.user.name = %q", got)
	}
	out := e.Output()
	if out["count"] != int64(2) {
		t.Errorf("Output()[count] = %#v", out["count"])
	}
	user, ok := out["user"].(map[string]any)
	if !ok || user["name"] != "ann" {
		t.Errorf("Output()[user] = %#v", out["user"])
	}
	if tags, ok := user["tags"].([]any); !ok || len(tags) != 2 || tags[1] != "b" {
		t.Errorf("Output()[user][tags] = %#v", user["tags"])
	}
}

// The output object set from a runScript file is visible to later ${} expressions.
func TestRunScriptSharesOutput(t *testing.T) {
	e := New("android")
	src := `
var base = json('{"n": 40}').n;
function add(a, b) { return a + b }
output.total = add(base, Number(TWO));
`
	if err := e.RunScript(src, "/scripts", scope(map[string]string{"TWO": "2"})); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Interpolate("${output.total}", nil); got != "42" {
		t.Errorf("output.total = %q", got)
	}
	if got, _ := e.Interpolate("${typeof add}", nil); got != "undefined" {
		t.Errorf("script function leaked: %q", got)
	}
}

// RunScript sets ScriptDir only while the script runs and reports script errors.
func TestRunScriptDirAndErrors(t *testing.T) {
	e := New("android")
	e.ScriptDir = "/before"
	var during string
	e.Log = func(string) { during = e.ScriptDir }
	if err := e.RunScript("console.log('x')", "/during", nil); err != nil {
		t.Fatal(err)
	}
	if during != "/during" {
		t.Errorf("ScriptDir during = %q", during)
	}
	if e.ScriptDir != "/before" {
		t.Errorf("ScriptDir after = %q, want restored", e.ScriptDir)
	}
	err := e.RunScript("throw new Error('nope')", "/x", nil)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "nope") {
		t.Fatalf("err = %v", err)
	}
	if e.ScriptDir != "/before" {
		t.Errorf("ScriptDir after error = %q", e.ScriptDir)
	}
}

// tb.platform comes from New, then from each scope that names one; an empty one keeps it.
func TestPlatformScope(t *testing.T) {
	e := New("web")
	if got, _ := e.Interpolate("${tb.platform}", nil); got != "web" {
		t.Errorf("initial platform = %q", got)
	}
	if got, _ := e.Interpolate("${tb.platform}", &Scope{Platform: "ios"}); got != "ios" {
		t.Errorf("scope platform = %q", got)
	}
	if got, _ := e.Interpolate("${tb.platform}", &Scope{}); got != "ios" {
		t.Errorf("empty scope platform = %q, want the previous one kept", got)
	}
	if got, _ := e.Interpolate("${maestro.platform}", &Scope{Platform: "android"}); got != "android" {
		t.Errorf("maestro.platform = %q", got)
	}
}

// tb.copiedText follows the scope's clipboard and is removed when a scope has none.
func TestCopiedTextScope(t *testing.T) {
	e := New("android")
	if got, _ := e.Interpolate("${tb.copiedText}", &Scope{}); got != "undefined" {
		t.Errorf("no clipboard = %q", got)
	}
	a, b, empty := "first", "second", ""
	if got, _ := e.Interpolate("${tb.copiedText}", &Scope{CopiedText: &a}); got != "first" {
		t.Errorf("copiedText = %q", got)
	}
	if got, _ := e.Interpolate("${maestro.copiedText}", &Scope{CopiedText: &b}); got != "second" {
		t.Errorf("maestro.copiedText = %q", got)
	}
	if got, _ := e.Interpolate("[${tb.copiedText}]", &Scope{CopiedText: &empty}); got != "[]" {
		t.Errorf("empty clipboard = %q", got)
	}
	if got, _ := e.Interpolate("${typeof tb.copiedText}", &Scope{}); got != "undefined" {
		t.Errorf("clipboard kept after a scope without one: %q", got)
	}
}

// `maestro` is the very same object as `tb`, so writes through one show through the other.
func TestMaestroAliasIsTb(t *testing.T) {
	e := New("android")
	if legacy.ScriptObject() != "maestro" {
		t.Fatalf("legacy script object = %q", legacy.ScriptObject())
	}
	if got, _ := e.Interpolate("${maestro === tb}", nil); got != "true" {
		t.Errorf("maestro === tb: %q", got)
	}
	if _, err := e.Eval("tb.custom = 'set via tb'", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Interpolate("${maestro.custom}", nil); got != "set via tb" {
		t.Errorf("maestro.custom = %q", got)
	}
}

// Permanent globals survive evaluations that try to delete or shadow them locally.
func TestPermanentGlobalsSurvive(t *testing.T) {
	e := New("android")
	if _, err := e.Eval("var http = 1; var faker = 2; var json = 3; http + faker + json", nil); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"${typeof http.get}":           "function",
		"${typeof faker.name}":         "function",
		"${json('[1]').length}":        "1",
		"${relativePoint(1, 1)}":       "100%,100%",
		"${typeof console.log}":        "function",
		"${typeof output}":             "object",
		"${typeof Date.now()}":         "number",
		"${new Array(3).length}":       "3",
		"${encodeURIComponent('a b')}": "a%20b",
	} {
		if got, err := e.Interpolate(in, nil); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", in, got, err, want)
		}
	}
}

// json() parses JSON text and throws on bad input.
func TestJSONHelper(t *testing.T) {
	e := New("android")
	cases := map[string]string{
		`${json('{"a":[1,2,{"b":"c"}]}').a[2].b}`: "c",
		`${json('"s"')}`:             "s",
		`${json('null')}`:            "null",
		`${json('true')}`:            "true",
		`${json('3.5') * 2}`:         "7",
		`${json(BODY).items.length}`: "2",
	}
	sc := scope(map[string]string{"BODY": `{"items":[1,2]}`})
	for in, want := range cases {
		if got, err := e.Interpolate(in, sc); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", in, got, err, want)
		}
	}
	if _, err := e.Eval("json('{bad')", nil); err == nil {
		t.Error("json of bad text did not throw")
	}
	if got, _ := e.Interpolate("${(function(){ try { json('x') } catch (e) { return e.name } })()}", nil); got != "SyntaxError" {
		t.Errorf("json error is catchable as SyntaxError: %q", got)
	}
}

// relativePoint turns fractions into a "x%,y%" point, rounding up to whole percents.
func TestRelativePoint(t *testing.T) {
	e := New("android")
	cases := map[string]string{
		"${relativePoint(0.5, 0.25)}":     "50%,25%",
		"${relativePoint(0, 0)}":          "0%,0%",
		"${relativePoint(1, 1)}":          "100%,100%",
		"${relativePoint(0.333, 0.666)}":  "34%,67%",
		"${relativePoint(0.101, 0.999)}":  "11%,100%",
		"${relativePoint(0.29, 0.57)}":    "29%,57%",
		"${relativePoint(0.5, 0.5)}":      "50%,50%",
		"${relativePoint(-0.555, 0.123)}": "-55%,13%",
	}
	for in, want := range cases {
		if got, err := e.Interpolate(in, nil); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", in, got, err, want)
		}
	}
}

// ceil rounds toward +Inf for positive and negative values.
func TestCeil(t *testing.T) {
	cases := map[float64]int{0: 0, 1: 1, 1.0001: 2, 49.5: 50, -0.5: 0, -1.5: -1, -2: -2, 99.99: 100}
	for in, want := range cases {
		if got := ceil(in); got != want {
			t.Errorf("ceil(%v) = %d, want %d", in, got, want)
		}
	}
}

// console.log joins its arguments in display form and hands the line to Log.
func TestConsoleLog(t *testing.T) {
	e := New("android")
	var lines []string
	e.Log = func(s string) { lines = append(lines, s) }
	src := `
console.log('hello', 'world');
console.log(1, 2.5, true, null, undefined);
console.log({b: 2, a: [1, 'x']});
console.log();
console.log('v=' + V);
`
	if _, err := e.Eval(src, scope(map[string]string{"V": "7"})); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"hello world",
		"1 2.5 true null undefined",
		`{a: [1, "x"], b: 2}`,
		"",
		"v=7",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q\nwant    %q", lines, want)
	}
	// With no Log hook, console.log is a silent no-op.
	e.Log = nil
	if v, err := e.Eval("console.log('x')", nil); err != nil || !goja.IsUndefined(v) {
		t.Errorf("console.log without Log: %v, %v", v, err)
	}
}

// The engine serializes concurrent evaluations: each sees only its own scope.
func TestConcurrentScopes(t *testing.T) {
	e := New("android")
	var wg sync.WaitGroup
	errs := make(chan string, 400)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := string(rune('a' + g))
			copied := "clip-" + name
			sc := &Scope{Env: map[string]string{"WHO": name}, Platform: "p" + name, CopiedText: &copied}
			for i := 0; i < 50; i++ {
				got, err := e.Interpolate("${WHO}/${tb.platform}/${tb.copiedText}", sc)
				want := name + "/p" + name + "/clip-" + name
				if err != nil || got != want {
					errs <- got + " != " + want
					return
				}
				if _, err := e.Eval("output.n = (output.n || 0) + 1", sc); err != nil {
					errs <- err.Error()
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
	if got := e.Output()["n"]; got != int64(400) {
		t.Errorf("shared output.n = %v, want 400", got)
	}
}

// Two engines are independent: output and tb do not leak between them.
func TestEnginesAreIndependent(t *testing.T) {
	a, b := New("android"), New("ios")
	if _, err := a.Eval("output.only = 'a'; tb.mark = 1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Interpolate("${typeof output.only}/${typeof tb.mark}/${tb.platform}", nil); got != "undefined/undefined/ios" {
		t.Errorf("engine b sees %q", got)
	}
}
