package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// args is a small flag parser that accepts flags anywhere among
// the positional arguments: `tb test flows/ --format junit -e A=1`.
type args struct {
	pos   []string
	vals  map[string][]string
	bools map[string]bool
	known map[string]flagKind // canonical flag name -> what it takes
	alias map[string]string
}

type flagSpec struct {
	names []string // first is canonical, e.g. {"env", "e"}
	kind  flagKind
}

// flagKind is what a flag takes. Numbers are checked as they are parsed: a value that is
// not one is bad input, never read as 0 (issue #62).
type flagKind int

const (
	boolFlag  flagKind = iota // no value: --json, or --json=false
	textFlag                  // any value: --format junit
	intFlag                   // a whole number, 0 or more: --retries 2
	floatFlag                 // a number, 0 or more: --scale 0.5
)

func parseArgs(argv []string, specs ...flagSpec) (*args, error) {
	a := &args{vals: map[string][]string{}, bools: map[string]bool{}, known: map[string]flagKind{}, alias: map[string]string{}}
	for _, s := range specs {
		for _, n := range s.names {
			a.alias[n] = s.names[0]
		}
		a.known[s.names[0]] = s.kind
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			a.pos = append(a.pos, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			a.pos = append(a.pos, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		val, hasVal := "", false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasVal = k, v, true
		}
		canon, ok := a.alias[name]
		if !ok {
			return nil, fmt.Errorf("unknown option %s", arg)
		}
		if kind := a.known[canon]; kind != boolFlag {
			if !hasVal {
				if i+1 >= len(argv) {
					return nil, fmt.Errorf("%s needs a value", arg)
				}
				i++
				val = argv[i]
			}
			if err := checkNumber(kind, val); err != nil {
				return nil, fmt.Errorf("%s: %v", strings.TrimSuffix(arg, "="+val), err)
			}
			a.vals[canon] = append(a.vals[canon], val)
		} else {
			b := true
			if hasVal {
				b, _ = strconv.ParseBool(val)
			}
			a.bools[canon] = b
		}
	}
	return a, nil
}

func (a *args) str(name string) string {
	v := a.vals[name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

func (a *args) list(name string) []string {
	var out []string
	for _, v := range a.vals[name] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// checkNumber checks the value of an intFlag or a floatFlag; any other kind takes any value.
func checkNumber(kind flagKind, val string) error {
	var n float64
	var err error
	switch kind {
	case intFlag:
		var i int
		i, err = strconv.Atoi(val)
		n = float64(i)
	case floatFlag:
		n, err = strconv.ParseFloat(val, 64)
		if err == nil && (math.IsNaN(n) || math.IsInf(n, 0)) {
			err = strconv.ErrSyntax
		}
	default:
		return nil
	}
	if err != nil {
		return fmt.Errorf("want a number, not %q", val)
	}
	if n < 0 {
		return fmt.Errorf("want 0 or more, not %q", val)
	}
	return nil
}

// num is an intFlag's value, or def when it is not given. parseArgs has checked it.
func (a *args) num(name string, def int) int {
	s := a.str(name)
	if s == "" {
		return def
	}
	n, _ := strconv.Atoi(s)
	return n
}

// float is a floatFlag's value, or def when it is not given. parseArgs has checked it.
func (a *args) float(name string, def float64) float64 {
	s := a.str(name)
	if s == "" {
		return def
	}
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

func (a *args) flag(name string) bool { return a.bools[name] }
