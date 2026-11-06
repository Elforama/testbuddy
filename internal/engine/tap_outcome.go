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

// A changed, existing result label beside a displaced control makes replay
// unsafe even without a native click event. This is deliberately conservative:
// it does not prove causality, and the flow must still assert its real outcome.
// Do not treat new banners, text elsewhere, geometry or an unchanged label as a
// result. Require the same uniquely identified leaf immediately after the same
// control in the same window in both already-captured snapshots.
func adjacentTapResultChanged(was *ui.Node, before *ui.Screen, now *ui.Node, after *ui.Screen) bool {
	if was == nil || now == nil || before == nil || after == nil || was.ID == "" ||
		!sameElementMoved(was, before, now, after) || was.Bounds == now.Bounds ||
		was.Window < 0 || was.Window >= len(before.Windows) || now.Window < 0 || now.Window >= len(after.Windows) {
		return false
	}
	w1, w2 := before.Windows[was.Window], after.Windows[now.Window]
	if w1.ID != w2.ID || w1.Package != w2.Package || w1.Type != w2.Type {
		return false
	}
	nextLabel := func(s *ui.Screen, target *ui.Node) *ui.Node {
		if target.Parent == nil {
			return nil
		}
		for i, n := range target.Parent.Children {
			if n != target || i+1 == len(target.Parent.Children) {
				continue
			}
			label := target.Parent.Children[i+1]
			if label.ID == "" || label.Label() == "" || label.Clickable || label.LongClickable ||
				label.Editable || label.Checkable || label.Scrollable || len(label.Children) != 0 ||
				(label.ShortClass() != "TextView" && label.ShortClass() != "StaticText") ||
				label.Bounds.Intersect(label.ClipRect(ui.Rect{R: s.Width, B: s.Height})).Empty() {
				return nil
			}
			count := 0
			for _, other := range s.Windows[target.Window].Nodes {
				if other.ID == label.ID {
					count++
				}
			}
			if count == 1 {
				return label
			}
			return nil
		}
		return nil
	}
	a, b := nextLabel(before, was), nextLabel(after, now)
	return a != nil && b != nil && a.ID == b.ID && a.Class == b.Class && a.Label() != b.Label()
}
