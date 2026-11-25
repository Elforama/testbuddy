package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

var replacedGrace = 3 * time.Second

func (r *Runner) SetSeen(s *ui.Screen) { r.seen = s }

func tapsByLookup(name string) bool {
	switch name {
	case "tapOn", "doubleTapOn", "longPressOn":
		return true
	}
	return false
}

func (r *Runner) seenTarget(sel *match.Selector) *ui.Node {
	if r.seen == nil || r.depth != 1 {
		return nil
	}
	n, all, err := match.Find(r.seen, sel)
	if err != nil || n == nil || len(all) != 1 || n.Label() == "" {
		return nil
	}
	return n
}

func sameScreen(seen, now *ui.Screen, was *ui.Node) bool {
	if seen.Foreground() != now.Foreground() {
		return false
	}
	labels := map[string]bool{}
	for _, n := range match.Visible(now) {
		labels[n.Label()] = true
	}
	kept, all := 0, 0
	for _, n := range match.Visible(seen) {
		if n == was || n.Label() == "" {
			continue
		}
		all++
		if labels[n.Label()] {
			kept++
		}
	}
	return all == 0 || 2*kept >= all
}

func transient(n *ui.Node) bool {
	l := strings.TrimSpace(n.Label())
	return strings.HasSuffix(l, "…") || strings.HasSuffix(l, "...") || strings.Contains(strings.ToLower(n.Class), "progress")
}

func inPlaceOf(was *ui.Node, s *ui.Screen) *ui.Node {
	var best *ui.Node
	bestShare := 0.0
	area := was.Bounds.Area()
	for _, n := range match.Visible(s) {
		if n.Label() == "" || n.Label() == was.Label() || transient(n) {
			continue
		}
		in := was.Bounds.Intersect(n.Bounds).Area()
		sameID := was.ID != "" && n.ID == was.ID
		if area == 0 || (!sameID && (float64(in) < 0.6*float64(area) || n.Bounds.Area() > 4*area)) {
			continue
		}
		if share := float64(in) / float64(area+n.Bounds.Area()-in); best == nil || share > bestShare {
			best, bestShare = n, share
		}
	}
	return best
}

type replacedWatch struct {
	seen   *ui.Screen
	was    *ui.Node
	now    *ui.Node
	since  time.Time
	reread bool
}

func (w *replacedWatch) see(s *ui.Screen) bool {
	if w.was == nil {
		return false
	}
	n := inPlaceOf(w.was, s)
	if n == nil || !sameScreen(w.seen, s, w.was) {
		w.now, w.since, w.reread = nil, time.Time{}, false
		return false
	}
	if w.since.IsZero() {
		w.since = time.Now()
	}
	w.now = n
	return time.Since(w.since) >= replacedGrace
}

func (w *replacedWatch) wake(deadline time.Time) time.Time {
	if w.since.IsZero() {
		return deadline
	}
	if t := w.since.Add(replacedGrace); t.After(time.Now()) && t.Before(deadline) {
		return t
	}
	return deadline
}

func (w *replacedWatch) message(sel *match.Selector) string {
	what := fmt.Sprintf("%q", w.now.Label())
	if c := w.now.ShortClass(); c != "" {
		what = c + " " + what
	}
	return fmt.Sprintf("%s (in your last look) is gone; at its place now: %s", sel.Describe(), what)
}
