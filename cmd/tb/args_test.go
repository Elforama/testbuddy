package main

import (
	"reflect"
	"strings"
	"testing"
)

// testSpecs mirrors a typical command's flags: values, bools, aliases.
var testSpecs = []flagSpec{
	{[]string{"env", "e"}, textFlag},
	{[]string{"device", "udid", "d"}, textFlag},
	{[]string{"format"}, textFlag},
	{[]string{"include-tags"}, textFlag},
	{[]string{"retries"}, intFlag},
	{[]string{"json"}, boolFlag},
	{[]string{"quiet", "q"}, boolFlag},
	{[]string{"headless"}, boolFlag},
}

// parseArgs accepts flags anywhere, aliases, -x=v, "--" and "-" as positional.
func TestParseArgs(t *testing.T) {
	cases := []struct {
		name  string
		argv  []string
		pos   []string
		vals  map[string][]string
		bools map[string]bool
	}{
		{name: "empty", argv: nil},
		{name: "positional only", argv: []string{"a.yaml", "dir/"}, pos: []string{"a.yaml", "dir/"}},
		{name: "flags before, between and after", argv: []string{"--format", "junit", "a", "-e", "X=1", "b", "--json"},
			pos: []string{"a", "b"}, vals: map[string][]string{"format": {"junit"}, "env": {"X=1"}}, bools: map[string]bool{"json": true}},
		{name: "short alias maps to canonical", argv: []string{"-e", "A=1", "-q"},
			vals: map[string][]string{"env": {"A=1"}}, bools: map[string]bool{"quiet": true}},
		{name: "long alias maps to canonical", argv: []string{"--udid", "ABC", "-d", "Pixel"},
			vals: map[string][]string{"device": {"ABC", "Pixel"}}},
		{name: "single dash form of a long flag", argv: []string{"-format", "html"}, vals: map[string][]string{"format": {"html"}}},
		{name: "equals value", argv: []string{"--format=junit", "-e=K=V"},
			vals: map[string][]string{"format": {"junit"}, "env": {"K=V"}}},
		{name: "equals empty value", argv: []string{"--format="}, vals: map[string][]string{"format": {""}}},
		{name: "repeated values accumulate", argv: []string{"-e", "A=1", "--env", "B=2", "-e=C=3"},
			vals: map[string][]string{"env": {"A=1", "B=2", "C=3"}}},
		{name: "value may start with a dash", argv: []string{"--format", "-weird", "-e", "--"},
			vals: map[string][]string{"format": {"-weird"}, "env": {"--"}}},
		{name: "lone dash is positional (stdin)", argv: []string{"-", "--json"}, pos: []string{"-"}, bools: map[string]bool{"json": true}},
		{name: "double dash ends flags", argv: []string{"--json", "--", "-e", "--format", "x"},
			pos: []string{"-e", "--format", "x"}, bools: map[string]bool{"json": true}},
		{name: "double dash at the end", argv: []string{"a", "--"}, pos: []string{"a"}},
		{name: "bool does not take the next word", argv: []string{"--json", "flows/"}, pos: []string{"flows/"}, bools: map[string]bool{"json": true}},
		{name: "bool with true value", argv: []string{"--json=true"}, bools: map[string]bool{"json": true}},
		{name: "bool with false value", argv: []string{"--json=false", "--quiet=0", "--headless=1"},
			bools: map[string]bool{"json": false, "quiet": false, "headless": true}},
		{name: "last bool wins", argv: []string{"--json", "--json=false"}, bools: map[string]bool{"json": false}},
		{name: "bool alias with value", argv: []string{"-q=false"}, bools: map[string]bool{"quiet": false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := parseArgs(c.argv, testSpecs...)
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", c.argv, err)
			}
			if len(a.pos) != 0 || len(c.pos) != 0 {
				if !reflect.DeepEqual(a.pos, c.pos) {
					t.Errorf("pos = %q, want %q", a.pos, c.pos)
				}
			}
			if c.vals == nil {
				c.vals = map[string][]string{}
			}
			if !reflect.DeepEqual(a.vals, c.vals) {
				t.Errorf("vals = %q, want %q", a.vals, c.vals)
			}
			if c.bools == nil {
				c.bools = map[string]bool{}
			}
			if !reflect.DeepEqual(a.bools, c.bools) {
				t.Errorf("bools = %v, want %v", a.bools, c.bools)
			}
		})
	}
}

// Unknown options and value flags without a value are errors naming the argument.
func TestParseArgsErrors(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"--nope"}, "unknown option --nope"},
		{[]string{"a", "-x"}, "unknown option -x"},
		{[]string{"--nope=1"}, "unknown option --nope=1"},
		{[]string{"--JSON"}, "unknown option --JSON"},
		{[]string{"--format"}, "--format needs a value"},
		{[]string{"a", "-e"}, "-e needs a value"},
		{[]string{"--udid"}, "--udid needs a value"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.argv, " "), func(t *testing.T) {
			a, err := parseArgs(c.argv, testSpecs...)
			if err == nil || err.Error() != c.want {
				t.Errorf("err = %v, want %q", err, c.want)
			}
			if a != nil {
				t.Error("args returned with an error")
			}
		})
	}
	// With no specs every flag is unknown.
	if _, err := parseArgs([]string{"--json"}); err == nil {
		t.Error("flag accepted with no specs")
	}
}

// str returns the last value, or "" when unset.
func TestArgsStr(t *testing.T) {
	a, _ := parseArgs([]string{"--format", "junit", "--format", "html"}, testSpecs...)
	if a.str("format") != "html" {
		t.Errorf("str = %q, want the last value", a.str("format"))
	}
	if a.str("env") != "" || a.str("unknown") != "" {
		t.Error("unset str not empty")
	}
}

// list splits comma lists across repeats, trimming spaces and dropping empties.
func TestArgsList(t *testing.T) {
	a, _ := parseArgs([]string{"--include-tags", "smoke, auth,,", "--include-tags", " slow ", "--include-tags", ","}, testSpecs...)
	if got := a.list("include-tags"); !reflect.DeepEqual(got, []string{"smoke", "auth", "slow"}) {
		t.Errorf("list = %q", got)
	}
	if got := a.list("device"); got != nil {
		t.Errorf("unset list = %q", got)
	}
	b, _ := parseArgs([]string{"-d", `alice="iPhone 17",bob=Pixel`}, testSpecs...)
	if got := b.list("device"); !reflect.DeepEqual(got, []string{`alice="iPhone 17"`, "bob=Pixel"}) {
		t.Errorf("role list = %q", got)
	}
}

// num reads the last value as an int, or the default when unset. A value that is not a
// whole number of 0 or more is a parse error, never the default (issue #62).
func TestArgsNum(t *testing.T) {
	cases := []struct {
		argv    []string
		def     int
		want    int
		wantErr string
	}{
		{argv: nil, def: 7, want: 7},
		{argv: []string{"--retries", "3"}, want: 3},
		{argv: []string{"--retries=0"}, def: 4, want: 0},
		{argv: []string{"--retries", "1", "--retries", "9"}, want: 9},
		{argv: []string{"--retries", "-2"}, wantErr: `--retries: want 0 or more, not "-2"`},
		{argv: []string{"--retries", "x"}, def: 5, wantErr: `--retries: want a number, not "x"`},
		{argv: []string{"--retries", "1.5"}, def: 5, wantErr: `--retries: want a number, not "1.5"`},
		{argv: []string{"--retries="}, def: 4, wantErr: `--retries: want a number, not ""`},
		{argv: []string{"--retries", "1", "--retries", "nine"}, wantErr: `--retries: want a number, not "nine"`},
	}
	for _, c := range cases {
		a, err := parseArgs(c.argv, testSpecs...)
		if c.wantErr != "" {
			if err == nil || err.Error() != c.wantErr {
				t.Errorf("%q: err %v, want %q", c.argv, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := a.num("retries", c.def); got != c.want {
			t.Errorf("num(%q, %d) = %d, want %d", c.argv, c.def, got, c.want)
		}
	}
}

// float reads a floatFlag: decimals are fine; words, negatives, NaN and Inf are not.
func TestArgsFloat(t *testing.T) {
	specs := []flagSpec{{[]string{"scale"}, floatFlag}}
	for v, want := range map[string]float64{"0.5": 0.5, "1": 1, "0": 0, ".25": 0.25} {
		a, err := parseArgs([]string{"--scale", v}, specs...)
		if err != nil || a.float("scale", 9) != want {
			t.Errorf("--scale %s: %v, err %v", v, a, err)
		}
	}
	for _, v := range []string{"half", "-0.1", "NaN", "Inf", "1/2", ""} {
		if _, err := parseArgs([]string{"--scale", v}, specs...); err == nil {
			t.Errorf("--scale %q: no error", v)
		}
	}
}

// flag is false unless set.
func TestArgsFlag(t *testing.T) {
	a, _ := parseArgs([]string{"--json"}, testSpecs...)
	if !a.flag("json") || a.flag("quiet") || a.flag("nonexistent") {
		t.Errorf("flags = %v", a.bools)
	}
}

// The real tb test flag set parses a representative command line.
func TestParseArgsTestCommandLine(t *testing.T) {
	specs := []flagSpec{
		{[]string{"env", "e"}, textFlag}, {[]string{"platform", "p"}, textFlag},
		{[]string{"device", "udid", "d"}, textFlag}, {[]string{"shard-split", "shards", "s"}, intFlag},
		{[]string{"format"}, textFlag}, {[]string{"output"}, textFlag}, {[]string{"json"}, boolFlag},
		{[]string{"reinstall-driver", "no-reinstall-driver"}, boolFlag}, {[]string{"help", "h"}, boolFlag},
	}
	argv := []string{"flows/", "-e", "USER=ann", "-p", "android", "--shards=2", "--format", "junit",
		"--no-reinstall-driver", "extra.yaml", "--output", "out.xml", "-e", "PASS=a=b"}
	a, err := parseArgs(argv, specs...)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.pos, []string{"flows/", "extra.yaml"}) {
		t.Errorf("pos = %q", a.pos)
	}
	if a.num("shard-split", 0) != 2 || a.str("platform") != "android" || a.str("output") != "out.xml" {
		t.Errorf("values = %v", a.vals)
	}
	if !reflect.DeepEqual(a.vals["env"], []string{"USER=ann", "PASS=a=b"}) {
		t.Errorf("env = %q", a.vals["env"])
	}
	if !a.flag("reinstall-driver") {
		t.Error("--no-reinstall-driver alias not recorded")
	}
}
