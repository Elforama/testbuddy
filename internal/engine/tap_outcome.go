package engine

import (
	"context"
	"strings"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

type tapAssertionKey struct{}

// A static, immediate assertion can confirm an action's outcome without another
// device read. Do not evaluate expressions early or carry a hint into a subflow.
func tapAssertionContext(ctx context.Context, cmds []*flow.Command, i int) context.Context {
	var next *flow.Command
	if cmds[i].Name == "tapOn" && i+1 < len(cmds) {
		c := cmds[i+1]
		if (c.Name == "assertVisible" || c.Name == "assertNotVisible") && !c.Optional &&
			c.Selector != nil && !c.Selector.Optional && staticTapAssertion(c.Selector) {
			next = c
		}
	}
	if previous, _ := ctx.Value(tapAssertionKey{}).(*flow.Command); next == nil && previous == nil {
		return ctx
	}
	return context.WithValue(ctx, tapAssertionKey{}, next)
}

func staticTapAssertion(s *flow.Selector) bool {
	if s == nil {
		return true
	}
	if s.CSS != "" || strings.Contains(s.Text+s.ID+s.Index, "${") {
		return false
	}
	for _, t := range s.Traits {
		if strings.Contains(t, "${") {
			return false
		}
	}
	for _, child := range []*flow.Selector{s.Below, s.Above, s.LeftOf, s.RightOf, s.ContainsChild, s.ChildOf} {
		if !staticTapAssertion(child) {
			return false
		}
	}
	for _, child := range s.ContainsDescendants {
		if !staticTapAssertion(child) {
			return false
		}
	}
	return true
}

func (r *Runner) tapAssertionChanged(ctx context.Context, before, after *ui.Screen) bool {
	c, _ := ctx.Value(tapAssertionKey{}).(*flow.Command)
	if c == nil || before == nil || after == nil {
		return false
	}
	sel, err := r.resolveSelector(c.Selector)
	if err != nil {
		return false
	}
	was, _, oldErr := match.Find(before, sel)
	now, _, newErr := match.Find(after, sel)
	if oldErr != nil || newErr != nil {
		return false
	}
	changed := (c.Name == "assertVisible" && was == nil && now != nil) ||
		(c.Name == "assertNotVisible" && was != nil && now == nil)
	if changed {
		r.tracef("the next assertion changed to its expected outcome; skipping tap replay")
	}
	return changed
}
