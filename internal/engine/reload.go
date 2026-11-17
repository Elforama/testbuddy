package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// A screen recreated by a configuration change (a turn, a fold, split screen, a window
// resized), or restored after its process died, can show a frame with no content first: an
// empty list, a spinner, a bare window. Its content comes a moment later, reloaded: as it
// was, or from the top of the list, its state lost (issue #284). That frame is quiet (the
// device settles on it), so a wait for absence judged on it passed: the element it waited to
// see gone was not there yet, and came back with the reload.
//
// So a wait for absence (assertNotVisible, a notVisible condition) does not judge on such a
// frame. It waits for the content, by the device's events, and judges on the screen that
// shows it; the wait is bounded by reloadWait, after which the screen is judged as it is (a
// list that is just empty). It waits only on evidence:
//   - the screen's context changed (reloadContext: the screen's size, the app's windows and
//     their bounds), and has not yet shown content since it did: the screen is a new one,
//     still loading. A list emptied in place (its last item deleted) is the same screen:
//     judged at once, as before. Labels are left out: a list that loses its last row stops
//     scrolling, and its id would then count as a label outside lists;
//   - the read shows no content (loadingWhy): the app's window bare, a list with no rows, a
//     spinner.
//
// A screen that loads in steps after its content first showed (content, then a second load
// that replaces it) is beyond what the runner can tell from the screen: the flow waits for
// the app's own sign that it is done (docs/architecture.md, "Checking a state after a
// restore").

// reloadWait bounds a wait for absence on a recreated screen still loading.
var reloadWait = 3 * time.Second

// loadingWhy says why s shows no content yet ("" when it shows some): the app's window has no
// nodes, or a list in it has no rows, or a spinner shows in it.
func loadingWhy(s *ui.Screen) string {
	if !hasAppContent(s) {
		return "the app's window has no content yet"
	}
	for _, n := range s.Nodes {
		if s.Windows[n.Window].Type != "app" {
			continue
		}
		if emptyList(n) {
			return "its list has no content yet (" + nodeDesc(n) + ")"
		}
		if busyIndicator(n) || n.Class == "ActivityIndicator" {
			return "it shows a progress indicator (" + nodeDesc(n) + ")"
		}
	}
	return ""
}

// emptyList: n is a list (scrollable, a collection, or a list view by its class) with no rows.
func emptyList(n *ui.Node) bool {
	if len(n.Children) > 0 || n.Label() != "" {
		return false
	}
	if n.Scrollable || n.Coll != nil {
		return true
	}
	c := n.ShortClass()
	return strings.HasSuffix(c, "RecyclerView") || c == "ListView" || c == "GridView"
}

// reloadContext names the screen for the rule above: its size, and its app windows (id,
// package, title, bounds). A turn, a fold, split screen and a recreated window each change it.
func reloadContext(s *ui.Screen) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%dx%d|", s.Platform, s.Width, s.Height)
	for _, w := range s.Windows {
		if w.Type != "app" {
			continue // a toast, the keyboard, the system bars: not the app's screen
		}
		fmt.Fprintf(&b, "%d:%q:%q:%q:%v|", w.ID, w.Type, w.Package, w.Title, w.Bounds)
	}
	return b.String()
}

// noteContext follows the screen's context across device reads (called under Runner.mu, from
// rememberScreen): a new context is ready once a read in it shows content.
func (r *Runner) noteContext(s *ui.Screen) {
	key := reloadContext(s)
	if key != r.ctxKey {
		r.ctxKey, r.ctxReady = key, loadingWhy(s) == ""
		return
	}
	if !r.ctxReady && loadingWhy(s) == "" {
		r.ctxReady = true
	}
}

// reloading says why a wait for absence must not judge on s yet ("" when it may): s is a new
// screen that has not shown its content since its context changed (see above).
func (r *Runner) reloading(s *ui.Screen) string {
	r.mu.Lock()
	ready, key := r.ctxReady, r.ctxKey
	r.mu.Unlock()
	if ready || key != reloadContext(s) {
		return ""
	}
	return loadingWhy(s)
}

// reloadJudged: a wait for absence waited reloadWait for the content of the screen in its
// context, which never came: the context is judged as it is from now on.
func (r *Runner) reloadJudged() {
	r.mu.Lock()
	r.ctxReady = true
	r.mu.Unlock()
}
