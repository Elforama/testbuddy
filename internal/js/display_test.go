package js

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// Display renders evaluated values the way flows see them: strings raw, numbers as JavaScript
// prints them, and objects/arrays in display form (not JSON).
func TestDisplayValues(t *testing.T) {
	e := New("android")
	cases := []struct{ src, want string }{
		{"'raw string'", "raw string"},
		{`'with "quotes"'`, `with "quotes"`},
		{"''", ""},
		{"true", "true"},
		{"false", "false"},
		{"null", "null"},
		{"undefined", "undefined"},
		{"0", "0"},
		{"-0", "0"},
		{"42", "42"},
		{"-7", "-7"},
		{"2.5", "2.5"},
		{"1/3", "0.3333333333333333"},
		{"1e21", "1e+21"},
		{"123456789012", "123456789012"},
		{"0/0", "NaN"},
		{"-1/0", "-Infinity"},
		{"[]", "[]"},
		{"[1, 2, 3]", "[1, 2, 3]"},
		{"['a', 'b']", `["a", "b"]`},
		{"[1.5, -2.25]", "[1.5, -2.25]"},
		{"[true, null]", "[true, null]"},
		{"[[1, 2], [3]]", "[[1, 2], [3]]"},
		{"({})", "{}"},
		{"({a: 1})", "{a: 1}"},
		{"({z: 1, a: 2, m: 3})", "{a: 2, m: 3, z: 1}"},
		{"({s: 'x', n: 1.5, b: false, u: null})", `{b: false, n: 1.5, s: "x", u: null}`},
		{"({list: [1, {k: 'v'}]})", `{list: [1, {k: "v"}]}`},
		{`({q: 'say "hi"\n'})`, `{q: "say \"hi\"\n"}`},
		{"[{a: 1}, {b: [2]}]", "[{a: 1}, {b: [2]}]"},
		{"[[[[[[1]]]]]]", "[[[[[...]]]]]"},
		{"({a: {b: {c: {d: {e: {f: 1}}}}}})", "{a: {b: {c: {d: {e: ...}}}}}"},
		{"[[[[1]]]]", "[[[[1]]]]"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			v, err := e.Eval(c.src, nil)
			if err != nil {
				t.Fatalf("Eval(%q): %v", c.src, err)
			}
			if got := Display(v); got != c.want {
				t.Errorf("Display(%s) = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

// Display handles Go-side values: nil, undefined, null and plain runtime values.
func TestDisplayGoValues(t *testing.T) {
	rt := goja.New()
	cases := []struct {
		name string
		v    goja.Value
		want string
	}{
		{"nil value", nil, "undefined"},
		{"undefined", goja.Undefined(), "undefined"},
		{"null", goja.Null(), "null"},
		{"string", rt.ToValue("s"), "s"},
		{"int64", rt.ToValue(int64(-12)), "-12"},
		{"float", rt.ToValue(0.5), "0.5"},
		{"bool", rt.ToValue(true), "true"},
		{"go slice", rt.ToValue([]any{"a", int64(1)}), `["a", 1]`},
		{"go map", rt.ToValue(map[string]any{"k": "v"}), `{k: "v"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Display(c.v); got != c.want {
				t.Errorf("Display = %q, want %q", got, c.want)
			}
		})
	}
}

// display formats nested Go values directly: quoted strings below the top level, floats
// without exponent noise, nil as null, and "..." past the depth limit.
func TestDisplayNested(t *testing.T) {
	cases := []struct {
		name  string
		x     any
		depth int
		want  string
	}{
		{"top-level string raw", "a b", 0, "a b"},
		{"nested string quoted", "a b", 1, `"a b"`},
		{"nil", nil, 0, "null"},
		{"float", 1.25, 1, "1.25"},
		{"whole float", 3.0, 1, "3"},
		{"int", 7, 1, "7"},
		{"bool", false, 1, "false"},
		{"empty slice", []any{}, 0, "[]"},
		{"empty map", map[string]any{}, 0, "{}"},
		{"too deep", []any{1}, 5, "..."},
		{"at the limit", []any{1}, 4, "[...]"},
		{"sorted keys", map[string]any{"b": 1, "a": 2}, 0, "{a: 2, b: 1}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := display(c.x, c.depth); got != c.want {
				t.Errorf("display(%#v, %d) = %q, want %q", c.x, c.depth, got, c.want)
			}
		})
	}
}

// A function displays as JavaScript source-ish text, never as a Go pointer.
func TestDisplayFunction(t *testing.T) {
	e := New("android")
	v, err := e.Eval("(function named() { return 1 })", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := Display(v)
	if strings.HasPrefix(got, "0x") || !strings.Contains(got, "function") {
		t.Errorf("Display(function) = %q", got)
	}
}

// Truthy is the condition rule for `true:` after interpolation.
func TestTruthyTable(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"\n\t", false},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"  false  ", false},
		{"undefined", false},
		{" undefined ", false},
		{"null", false},
		{"0", false},
		{"0.0", false},
		{"-0", false},
		{"+0", false},
		{"0e5", false},
		{" 0 ", false},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"-1", true},
		{"0.001", true},
		{"NaN", true},
		{"Infinity", true},
		{"yes", true},
		{"no", true},
		{"Undefined", true},
		{"NULL", true},
		{"false false", true},
		{"[]", true},
		{"{}", true},
		{"00", false},
	}
	for _, c := range cases {
		if got := Truthy(c.in); got != c.want {
			t.Errorf("Truthy(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// A condition's `true:` value goes through Interpolate then Truthy; this pins the combination.
func TestInterpolateThenTruthy(t *testing.T) {
	e := New("android")
	sc := scope(map[string]string{"FLAG": "false", "ON": "true", "ZERO": "0"})
	cases := map[string]bool{
		"${FLAG}":           false,
		"${ON}":             true,
		"${ZERO}":           false,
		"${MISSING}":        false,
		"${null}":           false,
		"${1 == 1}":         true,
		"${1 == 2}":         false,
		"${output.unset}":   false,
		"${'non-empty'}":    true,
		"${''}":             false,
		"${[].length}":      false,
		"${ON == 'true'}":   true,
		"${FLAG == 'true'}": false,
		"${Number(ZERO)}":   false,
		"${Number(ZERO)+1}": true,
	}
	for in, want := range cases {
		s, err := e.Interpolate(in, sc)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := Truthy(s); got != want {
			t.Errorf("Truthy(Interpolate(%s) = %q) = %v, want %v", in, s, got, want)
		}
	}
}
