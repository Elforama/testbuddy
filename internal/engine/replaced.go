package engine

import (
	"fmt"
	"time"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

var replacedGrace = 2 * time.Second

func (r *Runner) SetSeen(s *ui.Screen) { r.seen = s }

func (r *Runner) seenTarget(sel *match.Selector) *ui.Node {
	if r.seen == nil {
		return nil
	}
	n, all, err := match.Find(r.seen, sel)
	if err != nil || n == nil || len(all) != 1 || n.Label() == "" {
		return nil
	}
	return n
}

func inPlaceOf(was *ui.Node, s *ui.Screen) *ui.Node {
	var best *ui.Node
	bestShare := 0.0
	area := was.Bounds.Area()
	for _, n := range match.Visible(s) {
		if n.Label() == "" || n.Label() == was.Label() {
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
	was   *ui.Node
	now   *ui.Node
	since time.Time
}

func (w *replacedWatch) see(s *ui.Screen) bool {
	if w.was == nil {
		return false
	}
	n := inPlaceOf(w.was, s)
	if n == nil {
		w.now, w.since = nil, time.Time{}
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
	if t := w.since.Add(replacedGrace); t.Before(deadline) {
		return t
	}
	return deadline
}

func (w *replacedWatch) message(sel *match.Selector) string {
	return fmt.Sprintf("%s (in your last look) is gone; at its place now: %s", sel.Describe(), nodeDesc(w.now))
}
