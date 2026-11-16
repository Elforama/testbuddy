package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/changes"
	"github.com/elforama/testbuddy/internal/ui"
)

// Before its first swipe, a scroll that is looking for an element (scrollUntilVisible in either
// direction, scrollUntilVisibleIn, and the scroll that brings a tap's target back: every
// command that answers "not found" by moving the screen goes through scrollTo) must have read
// a screen that shows the content it would scroll (issue #291). Right after a launch, the
// first read can come before the app's window, or before its list has laid out its rows: the
// device's settle is event-driven, and on Android it can end on the window event that comes
// before the rows. A target near the top of the list was then "not visible", the drag went out
// as the rows arrived, and carried it off the top. Such a read says "not ready", not "not
// found": the scroll waits for the screen to change, as a lookup does, before its first swipe.

// unready says why s cannot yet tell whether a scroll is needed ("" when it can), with the
// first swipe starting at (x, y): the app's window is not on screen, or has no content, or the
// list under the swipe has none (a container with no rows yet). A screen with no list under
// the swipe is judged as it is: a screen that scrolls without reporting a list must not wait.
func unready(s *ui.Screen, x, y int) string {
	if !hasAppContent(s) {
		return "the app's window is not on screen yet"
	}
	if box := scrollContainer(s, x, y); box != nil && len(box.Children) == 0 && box.Label() == "" {
		return "its list has no content yet (" + nodeDesc(box) + ")"
	}
	return ""
}

// hasAppContent: s has an app window with nodes in it.
func hasAppContent(s *ui.Screen) bool {
	for _, w := range s.Windows {
		if w.Type == "app" && len(w.Nodes) > 0 {
			return true
		}
	}
	return false
}

// unreadyWait bounds the wait for a screen to become ready to scroll, for a screen that keeps
// changing without ever showing the list's content (a spinner over an empty list): after it,
// the screen is judged as it is, and scrolled as before.
const unreadyWait = 3 * time.Second

// scrollReady is the rule above, asked before a scroll's first swipe with the read s it was
// decided on, the swipe's start (x, y), the scroll's deadline, and w, the scroll's watch of
// the device (subscribed before the first read). again: it waited; read the screen again and
// decide afresh; else the swipe may go. It waits only when the screen gives a reason:
//   - unready (above): it waits, by the same rule as a lookup (changes.Watch: a change that
//     settles, or RestlessAfter of changes; the silence net), until the screen is ready, or
//     the device has said nothing for the net's gap (the content has stopped changing: an
//     empty list is just empty), or unreadyWait has passed;
//   - news since the read (the screen changed after it, a launch still drawing): it waits once
//     for that change to settle (at most RestlessAfter) and decides on a fresh read.
//
// A screen that is settled and shows its list's content swipes at once: no wait, no read.
// st holds what scrollReady knows across the reads of one scroll.
func (r *Runner) scrollReady(ctx context.Context, w *changes.Watch, st *readyState, s *ui.Screen, x, y int, deadline time.Time) (again bool, err error) {
	why := unready(s, x, y)
	news := r.Drv.Seq() > s.Seq
	if why == "" && !st.since.IsZero() && !st.said {
		st.said = true
		r.tracef("ready to scroll after %s", since(st.since))
	}
	switch {
	case why == "" && !news:
		return false, nil
	case why == "" && st.newsWaited:
		// A screen that never stops changing (a ticker): judged on the read in hand.
		return false, nil
	case why != "" && st.silent > 0:
		r.tracef("still not ready after %s without a change (%s): scrolling it as it is", st.silent, why)
		return false, nil
	case why != "" && !st.since.IsZero() && time.Since(st.since) >= unreadyWait:
		r.tracef("still not ready after %s (%s): scrolling it as it is", since(st.since), why)
		return false, nil
	case !time.Now().Before(deadline):
		return false, nil
	}
	if why != "" {
		if st.since.IsZero() {
			st.since = time.Now()
			r.tracef("not ready to scroll: %s; waiting for the screen to change before the first swipe", why)
		}
	} else {
		st.newsWaited = true
		r.tracef("the screen changed after the read the scroll was decided on: reading it again before the first swipe")
	}
	dl := deadline
	if why != "" {
		dl = minTime(dl, st.since.Add(unreadyWait))
	}
	silence, err := w.Wait(ctx, s.Seq, dl, false)
	if err != nil {
		return false, err
	}
	if silence > 0 {
		st.silent = silence
		r.invalidate() // the net's re-read: the screen may have changed unreported
	}
	return true, nil
}

// readyState is what scrollReady keeps across one scroll's reads.
type readyState struct {
	since      time.Time     // when the screen was first found not ready
	silent     time.Duration // a wait ended in silence: the device said nothing for this long
	newsWaited bool          // waited once for news that came after a ready read
	said       bool          // said that the screen became ready
}
