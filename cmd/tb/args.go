package main

import (
	"fmt"
	"strconv"
	"strings"
)

// args is a small flag parser that accepts flags anywhere among
// the positional arguments: `tb test flows/ --format junit -e A=1`.
type args struct {
	pos   []string
	vals  map[string][]string
	bools map[string]bool
	known map[string]bool // flag name -> takes a value
	alias map[string]string
}

type flagSpec struct {
	names []string // first is canonical, e.g. {"env", "e"}
	value bool
}

func parseArgs(argv []string, specs ...flagSpec) (*args, error) {
	a := &args{vals: map[string][]string{}, bools: map[string]bool{}, known: map[string]bool{}, alias: map[string]string{}}
	for _, s := range specs {
		for _, n := range s.names {
			a.alias[n] = s.names[0]
		}
		a.known[s.names[0]] = s.value
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
		if a.known[canon] {
			if !hasVal {
				if i+1 >= len(argv) {
					return nil, fmt.Errorf("%s needs a value", arg)
				}
				i++
				val = argv[i]
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

func (a *args) num(name string, def int) int {
	s := a.str(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (a *args) flag(name string) bool { return a.bools[name] }
