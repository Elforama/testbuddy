package main

import (
	"reflect"
	"strings"
	"testing"
)

// testSpecs mirrors a typical command's flags: values, bools, aliases.
var testSpecs = []flagSpec{
	{[]string{"env", "e"}, true},
	{[]string{"device", "udid", "d"}, true},
	{[]string{"format"}, true},
	{[]string{"include-tags"}, true},
	{[]string{"retries"}, true},
	{[]string{"json"}, false},
	{[]string{"quiet", "q"}, false},
	{[]string{"headless"}, false},
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

// num parses the last value as an int, falling back to the default when unset or invalid.
func TestArgsNum(t *testing.T) {
	cases := []struct {
		argv []string
		def  int
		want int
	}{
		{nil, 7, 7},
		{[]string{"--retries", "3"}, 0, 3},
		{[]string{"--retries", "-2"}, 0, -2},
		{[]string{"--retries", "x"}, 5, 5},
		{[]string{"--retries", "1.5"}, 5, 5},
		{[]string{"--retries="}, 4, 4},
		{[]string{"--retries", "1", "--retries", "9"}, 0, 9},
	}
	for _, c := range cases {
		a, err := parseArgs(c.argv, testSpecs...)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.num("retries", c.def); got != c.want {
			t.Errorf("num(%q, %d) = %d, want %d", c.argv, c.def, got, c.want)
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
		{[]string{"env", "e"}, true}, {[]string{"platform", "p"}, true},
		{[]string{"device", "udid", "d"}, true}, {[]string{"shard-split", "shards", "s"}, true},
		{[]string{"format"}, true}, {[]string{"output"}, true}, {[]string{"json"}, false},
		{[]string{"reinstall-driver", "no-reinstall-driver"}, false}, {[]string{"help", "h"}, false},
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
