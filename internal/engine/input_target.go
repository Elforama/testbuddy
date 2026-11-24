package engine

import (
	"context"
	"regexp"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) ensureRequestedInputTarget(ctx context.Context) error {
	target := r.inputTarget
	if target == nil || r.Drv.Platform() != "android" {
		return nil
	}
	matches := func(s *ui.Screen, n *ui.Node) bool {
		return n != nil && n.ID == target.ID && n.Window >= 0 && n.Window < len(s.Windows) && s.Windows[n.Window].ID == target.Window
	}
	s := r.LastScreen()
	if s == nil {
		return nil
	}
	f := focusedAppField(s)
	if f == nil || f.ID == "" || matches(s, f) {
		return nil
	}
	r.invalidate()
	var err error
	if s, err = r.read(ctx); err != nil {
		return err
	}
	f = focusedAppField(s)
	if f == nil || f.ID == "" || matches(s, f) {
		return nil
	}
	r.tracef("field %s still has focus instead of requested %s; refocusing before typing", f.ID, target.ID)
	if err := r.tap(ctx, &flow.Command{Name: "tapOn", Selector: &flow.Selector{ID: regexp.QuoteMeta(target.ID)}}); err != nil {
		return err
	}
	r.invalidate()
	if s, err = r.read(ctx); err != nil {
		return err
	}
	if f := focusedAppField(s); f != nil && f.ID != "" && !matches(s, f) {
		return failf("Requested field %s did not take focus; refusing to type into another field", target.ID)
	}
	return nil
}

func focusedAppField(s *ui.Screen) *ui.Node {
	app := func(n *ui.Node) bool {
		return n.Focused && !(n.Window >= 0 && n.Window < len(s.Windows) && s.Windows[n.Window].Type == "keyboard")
	}
	for _, n := range s.Nodes {
		if app(n) && n.Editable {
			return n
		}
	}
	for _, n := range s.Nodes {
		if app(n) {
			return n
		}
	}
	return nil
}
