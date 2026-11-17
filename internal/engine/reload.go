package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// A screen recreated by a configuration change (a turn, a fold, split screen, a window
// resized) can show a frame with no content first: an empty list, a spinner, a bare window.
// Its content comes a moment later, reloaded: as it was, or from the top of the list, its
// state lost (issue #284). That frame is quiet (the device settles on it), so a wait for
// absence judged on it passed: the element it waited to see gone was not there yet, and came
// back with the reload.
//
// So a wait for absence (assertNotVisible, a notVisible condition) does not judge on such a
// frame. It waits for the content, by the device's events, and judges on the screen that
// shows it; the wait is bounded by reloadWait, after which the screen is judged as it is (a
// list that is just empty). It waits only on evidence of both:
//   - a new size: the display's size, or the bounds of the app's bottom-most window, changed
//     (reloadContext), and no read since has shown content. Nothing else counts (issue #284
//     review): a dialog, a popup or a sheet layered over the screen, a new Activity, a web
//     page's new URL or title come with no change of size, and are judged at once, as before.
//     Nor does the first size a run reads, nor a read with no app window (a switch between
//     Activities);
//   - no content (loadingWhy) in the bottom-most app window (a dialog's spinner is not the
//     screen's): the window bare, a list with no rows, a spinner that shows no progress value,
//     or a window listed but not readable yet (Screen.Unread: Android's replacement window,
//     while the old one still shows its content at the new size).
//
// An empty state after a turn (an empty inbox) looks like a reload: it costs up to reloadWait,
// once per turn. So does an empty table on iOS, whose tables are always scrollable. A screen
// that loads in steps after its content first showed (content, then a second load that
// replaces it) is beyond what the runner can tell from the screen: the flow waits for the
// app's own sign that it is done (docs/architecture.md, "Checking a state after a restore").

// reloadWait bounds a wait for absence on a recreated screen still loading.
var reloadWait = 3 * time.Second

// screenWindow is the bottom-most app window of s (the screen under any dialog), or nil.
func screenWindow(s *ui.Screen) *ui.Window {
	for i := len(s.Windows) - 1; i >= 0; i-- {
		if s.Windows[i].Type == "app" {
			return s.Windows[i]
		}
	}
	return nil
}

// loadingWhy says why s shows no content yet ("" when it shows some): its bottom-most app
// window has no nodes, or a list in it has no rows, or a spinner shows in it.
func loadingWhy(s *ui.Screen) string {
	w := screenWindow(s)
	if w == nil || len(w.Nodes) == 0 {
		return "the app's window has no content yet"
	}
	if s.Unread > 0 {
		// After a turn, the old window can still show its content at the new size while its
		// replacement, listed, has no root yet (seen on the bench).
		return fmt.Sprintf("a window of it cannot be read yet (%d without a root)", s.Unread)
	}
	for _, n := range w.Nodes {
		if emptyList(n) {
			return "its list has no content yet (" + nodeDesc(n) + ")"
		}
		if spinning(n) {
			return "it shows a progress indicator (" + nodeDesc(n) + ")"
		}
	}
	return ""
}

// spinning: n is an indeterminate progress indicator: Android's ProgressBar (Compose's
// progress indicators report as one too), iOS's ActivityIndicator or ProgressIndicator, with
// no progress value. A determinate one says how far it is (Android's state description "40%"
// or "40 percent", iOS's value "40%"): an upload's bar, a step indicator, not a load.
func spinning(n *ui.Node) bool {
	if !busyIndicator(n) && n.Class != "ActivityIndicator" && n.Class != "ProgressIndicator" {
		return false
	}
	return !strings.ContainsAny(n.State+n.Value+n.Text, "0123456789")
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

// reloadContext names the screen's size for the rule above: the display's, and the bounds of
// its bottom-most app window (split screen and a resized window change only these). "" when
// s has no app window: such a read says nothing of the size.
func reloadContext(s *ui.Screen) string {
	w := screenWindow(s)
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%dx%d|%v", s.Width, s.Height, w.Bounds)
}

// noteContext follows the screen's size across device reads (called under Runner.mu, from
// rememberScreen): after a change of size, the screen is ready once a read shows content.
// The first size seen is ready: a run's first screen is no restore.
//
// A turn can be read before the app's window is replaced: the display has turned, the old
// Activity still shows its content at the new size, and its replacement comes a moment
// later, empty (seen on the bench, issue #284). So the first replacement of the bottom-most
// app window after a change of size is the recreation, and the content is looked for again
// from it. Later replacements at that size (a new Activity opened by the flow) are not.
func (r *Runner) noteContext(s *ui.Screen) {
	key := reloadContext(s)
	if key == "" {
		return
	}
	win := screenWindow(s).ID
	switch {
	case r.ctxKey == "":
		r.ctxKey, r.ctxReady, r.ctxWin, r.ctxRebuilt = key, true, win, true
	case key != r.ctxKey:
		r.ctxKey, r.ctxReady = key, loadingWhy(s) == ""
		r.ctxRebuilt = win != r.ctxWin
		r.ctxWin = win
	case win != r.ctxWin:
		r.ctxWin = win
		if !r.ctxRebuilt {
			r.ctxRebuilt = true
			r.ctxReady = loadingWhy(s) == ""
		}
	}
	if !r.ctxReady && loadingWhy(s) == "" {
		r.ctxReady = true
	}
}

// reloading says why a wait for absence must not judge on s yet ("" when it may): s is a
// screen of a new size that has not shown its content since its size changed (see above).
func (r *Runner) reloading(s *ui.Screen) string {
	r.mu.Lock()
	ready, key := r.ctxReady, r.ctxKey
	r.mu.Unlock()
	if ready || key == "" {
		return ""
	}
	// A read with no app window while the new size has shown no content (the window being
	// replaced, its root not readable yet) is loading too.
	if now := reloadContext(s); now != "" && now != key {
		return ""
	}
	return loadingWhy(s)
}

// reloadJudged: a wait for absence waited reloadWait for the content of the screen at its
// size, which never came: that size is judged as it is from now on.
func (r *Runner) reloadJudged() {
	r.mu.Lock()
	r.ctxReady = true
	r.mu.Unlock()
}
