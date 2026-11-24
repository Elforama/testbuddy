package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/changes"
	"github.com/elforama/testbuddy/internal/ui"
)

func unready(s *ui.Screen, x, y int) string {
	if !hasAppContent(s) {
		return "the app's window is not on screen yet"
	}
	if box := scrollContainer(s, x, y); box != nil && len(box.Children) == 0 && box.Label() == "" {
		return "its list has no content yet (" + nodeDesc(box) + ")"
	}
	return ""
}

func hasAppContent(s *ui.Screen) bool {
	for _, w := range s.Windows {
		if w.Type == "app" && len(w.Nodes) > 0 {
			return true
		}
	}
	return false
}

const unreadyWait = 3 * time.Second

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
		r.reread(ctx)
	}
	return true, nil
}

type readyState struct {
	since      time.Time
	silent     time.Duration
	newsWaited bool
	said       bool
}
