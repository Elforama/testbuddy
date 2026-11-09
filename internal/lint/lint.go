// Package lint finds flow steps that run but are fragile: coordinates, match-order
// indexes, text that reads as an unintended regex, assertions that cannot fail, and flows
// that assert nothing. It reads a parsed flow; no device is needed.
package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
)

// Warning is one fragile step (or, with no Line, the flow as a whole).
type Warning struct {
	Code    string `json:"code"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Step    int    `json:"step,omitempty"` // 1-based top-level step; 0 for nested commands and hooks
	Message string `json:"message"`
}

// Flow lints a parsed flow: its hooks, its commands and their inline nested commands. The
// commands of a runFlow file are not included (lint that file on its own).
func Flow(f *flow.Flow) []Warning {
	l := &linter{}
	l.commands(f.Config.OnFlowStart, false)
	l.commands(f.Commands, true)
	l.commands(f.Config.OnFlowComplete, false)
	if !l.asserts {
		l.out = append(l.out, Warning{Code: "missing_assertion",
			Message: "Add an assertion to verify the result of the flow's actions."})
	}
	return l.out
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
		// Single-quoted: YAML keeps backslashes there as written.
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

// sel is a selector to walk with its nested relations.
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

// selectors are every selector a command holds: its target, a swipe's anchor, a scroll's
// list, a screenshot's crop, and those of its conditions.
func selectors(c *flow.Command) []sel {
	out := []sel{{c.Selector}, {c.From}, {c.In}, {c.CropOn}}
	for _, cond := range []*flow.Condition{c.Cond, c.When, c.While} {
		if cond != nil {
			out = append(out, sel{cond.Visible}, sel{cond.NotVisible})
		}
	}
	return out
}

// intendedRegex: signs that a text was written as a regex on purpose.
var intendedRegex = regexp.MustCompile(`\.[*+?]|\\|\[|\||^\^|\$$|\{\d|\(\?`)

// unintendedRegex reports a value that tb reads as a regex which, as a regex, does not
// match the value itself, written with no sign of meaning a regex: "Sign in?" matches
// "Sign in" and "Sign i". A text selector still finds its literal text (texts also match
// as written); an id selector does not. Interpolated values are skipped: they are known
// only when the flow runs.
func unintendedRegex(v string) bool {
	if v == "" || strings.Contains(v, "${") || !strings.ContainsAny(v, `.+*?()[]{}|^$\`) {
		return false
	}
	if intendedRegex.MatchString(v) || !match.Compiles(v) {
		return false
	}
	return !match.RegexMatches(v, v)
}
