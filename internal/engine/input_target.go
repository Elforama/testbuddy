package engine

import (
	"context"
	"regexp"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// A known different focused field is not a typing fallback. Re-resolve the
// editable control the user requested before sending any characters. Unknown
// provider focus, and focus inside the keyboard's own window, still use the
// existing first-key verification fallback.
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
	// A late focus event may already have corrected the cached observation.
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

// focusedAppField is focusedField outside the keyboard's window. A keyboard that has just
// opened can report one of its own controls as focused (Gboard's header) before the tapped
// field reports focus: that is not another field the text could go to.
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
