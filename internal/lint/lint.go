package lint

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
)

type Warning struct {
	Code    string `json:"code"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Step    int    `json:"step,omitempty"`
	Message string `json:"message"`
}

func Flow(f *flow.Flow) []Warning {
	l := &linter{out: keys(f)}
	l.commands(f.Config.OnFlowStart, false)
	l.commands(f.Commands, true)
	l.commands(f.Config.OnFlowComplete, false)
	if !l.asserts {
		l.out = append(l.out, Warning{Code: "missing_assertion",
			Message: "Add an assertion to verify the result of the flow's actions."})
	}
	return l.out
}

func keys(f *flow.Flow) []Warning {
	var out []Warning
	for k := range f.Config.Ext {
		if slices.Contains(flow.HeaderKeys, k) {
			continue
		}
		msg := fmt.Sprintf("Header key %q is not one tb reads, so it does nothing.", k)
		if near := flow.NearestHeaderKey(k); near != "" {
			msg += " Did you mean " + near + "?"
		} else {
			msg += " Remove it, unless another tool reads it."
		}
		out = append(out, Warning{Code: "unknown_header_key", File: f.Path, Line: f.Config.ExtLines[k], Message: msg})
	}
	for _, d := range f.DuplicateKeys {
		out = append(out, Warning{Code: "duplicate_key", File: f.Path, Line: d.Line,
			Message: fmt.Sprintf("%q is written twice in the same map (first on line %d); only the last value is used. Remove one.", d.Key, d.First)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

type linter struct {
	out     []Warning
	asserts bool
}

func (l *linter) commands(cmds []*flow.Command, top bool) {
	for i, c := range cmds {
		step := 0
		if top {
			step = i + 1
		}
		l.command(c, step)
	}
}

func isAssertion(name string) bool {
	return strings.HasPrefix(name, "assert") || name == "extendedWaitUntil"
}

func (l *linter) command(c *flow.Command, step int) {
	add := func(code, msg string) {
		l.out = append(l.out, Warning{Code: code, File: c.File, Line: c.Line, Step: step, Message: msg})
	}
	switch {
	case c.Name == "swipe" && (c.Point != "" || c.Start != ""):
		add("coordinate_swipe", "Swipe depends on coordinates. Prefer a direction or an element anchor.")
	case c.Name == "drag" && (c.Start != "" || c.End != ""):
		add("coordinate_swipe", "Drag depends on coordinates. Prefer elements: from and to.")
	case c.Point != "":
		if strings.Contains(c.Point, "%") {
			add("coordinate_tap", "Tap depends on coordinates. Prefer an element selector.")
		} else {
			add("coordinate_tap", "Tap depends on screen pixels, which also change with screen size. Prefer an element selector.")
		}
	}
	indexed := false
	type regexValue struct {
		v  string
		id bool
	}
	var regexes []regexValue
	for _, s := range selectors(c) {
		s.walk(func(s *flow.Selector) {
			indexed = indexed || s.Index != ""
			if unintendedRegex(s.Text) {
				regexes = append(regexes, regexValue{s.Text, false})
			}
			if unintendedRegex(s.ID) {
				regexes = append(regexes, regexValue{s.ID, true})
			}
		})
	}
	if indexed {
		add("indexed_selector", "Selector depends on match order. Prefer a unique selector.")
	}
	for _, r := range regexes {
		esc := "'" + strings.ReplaceAll(regexp.QuoteMeta(r.v), "'", "''") + "'"
		if r.id {
			add("regex_text", fmt.Sprintf("id %q is read as a regex that does not match itself, so it never finds an element with that id. Escape it (single-quoted in YAML): %s.", r.v, esc))
		} else {
			add("regex_text", fmt.Sprintf("%q is also read as a regex, so it matches other text too. To match only the text as written, escape it (single-quoted in YAML): %s.", r.v, esc))
		}
	}
	if isAssertion(c.Name) {
		l.asserts = true
		if c.Optional {
			add("optional_assertion", "An optional assertion never fails the flow. Drop optional, or use runFlow with when: to branch.")
		}
	}
	l.commands(c.Commands, false)
	for _, role := range c.ParallelOrder {
		l.commands(c.Parallel[role], false)
	}
}

type sel struct{ *flow.Selector }

func (s sel) walk(fn func(*flow.Selector)) {
	if s.Selector == nil {
		return
	}
	fn(s.Selector)
	for _, n := range []*flow.Selector{s.Below, s.Above, s.LeftOf, s.RightOf, s.ContainsChild, s.ChildOf} {
		sel{n}.walk(fn)
	}
	for _, n := range s.ContainsDescendants {
		sel{n}.walk(fn)
	}
}

func selectors(c *flow.Command) []sel {
	out := []sel{{c.Selector}, {c.From}, {c.To}, {c.In}, {c.CropOn}}
	for _, cond := range []*flow.Condition{c.Cond, c.When, c.While} {
		if cond != nil {
			out = append(out, sel{cond.Visible}, sel{cond.NotVisible})
		}
	}
	return out
}

var intendedRegex = regexp.MustCompile(`\.[*+?]|\\|\[|\||^\^|\$$|\{\d|\(\?`)

func unintendedRegex(v string) bool {
	if v == "" || strings.Contains(v, "${") || !strings.ContainsAny(v, `.+*?()[]{}|^$\`) {
		return false
	}
	if intendedRegex.MatchString(v) || !match.Compiles(v) {
		return false
	}
	return !match.RegexMatches(v, v)
}
