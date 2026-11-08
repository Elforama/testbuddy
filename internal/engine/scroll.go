package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) scrollUntilVisible(ctx context.Context, c *flow.Command) error {
	sel, err := r.resolveSelector(c.Selector)
	if err != nil {
		return err
	}
	return r.scrollTo(ctx, c, sel)
}

// scrollTo is scrollUntilVisible for the resolved selector sel (c gives the rest).
func (r *Runner) scrollTo(ctx context.Context, c *flow.Command, sel *match.Selector) error {
	d := r.Drv
	var err error
	// scrollUntilVisibleIn: the element is in the list that holds this one.
	var in *match.Selector
	if c.In != nil {
		if in, err = r.resolveSelector(c.In); err != nil {
			return err
		}
	}
	// listedIn is the node that tells which list to drag: the element itself once listed, else
	// scrollUntilVisibleIn's list item, else an off-screen listing of the element.
	listedIn := func(s *ui.Screen, n *ui.Node) *ui.Node {
		if n != nil {
			return n
		}
		if in != nil {
			if l, _, err := r.match(ctx, s, in); err == nil && l != nil {
				return l
			}
		}
		l, _ := match.FindListed(s, sel)
		return l
	}
	t, err := r.eval(c.Timeout)
	if err != nil {
		return err
	}
	timeout, err := parseMs(t, 20*time.Second)
	if err != nil {
		return err
	}
	if timeout < 0 {
		timeout = 20 * time.Second
	}
	speed := 40
	if c.Speed != "" {
		sp, err := r.eval(c.Speed)
		if err != nil {
			return err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(sp), 64)
		if err != nil || f < 0 || f > 100 {
			return &invalidField{msg: fmt.Sprintf("speed must be 0-100, got %q", sp)}
		}
		speed = int(f)
	}
	dur := time.Duration(1000*(100-speed)/100+1) * time.Millisecond
	need := 0.0
	if c.VisibilityPct >= 100 {
		need = 1.0
	}
	w, h := d.Size()
	screen := ui.Rect{R: w, B: h}
	vertical := c.Direction == "DOWN" || c.Direction == "UP"
	// Drag opposite to the way the content should travel: up and down from the center to near
	// the edge, sideways across 70% of the width (past half, so a paging list turns its page).
	x1, y1 := w/2, h/2
	var x2, y2 int
	switch c.Direction {
	case "DOWN":
		x2, y2 = w/2, h/10
	case "UP":
		x2, y2 = w/2, h*9/10
	case "RIGHT":
		x1, x2, y2 = w*85/100, w*15/100, h/2
	default: // LEFT
		x1, x2, y2 = w*15/100, w*85/100, h/2
	}
	fx1, fy1 := w/2, h/2 // a flinging swipe from the center: the fallback below
	// `speed` sets a swipe's duration. These drags do not fling, so it changes little: how far
	// the content moves is the drag's length.

	// Each swipe is a drag that does not fling (ActionOpts.Drag): the content stops as the finger
	// lifts. A flinging swipe moves it farther, but it coasts on for 0.5-2 s that would have to
	// be waited out before each look. So the screen is read right after each drag, dragged
	// again at once while the element is not there, and only checked for rest once it shows.
	drag := driver.ActionOpts{NoSettle: true, Drag: true}
	events, stop := d.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	centerTries := 0
	placed := false       // the drag's start moved into the list (iOS, up and down)
	var dragged time.Time // when the last drag began; zero once the screen is known to be still
	var before uint64     // what the list under the drag looked like when it began
	var stalled time.Time // since when drags have moved nothing
	settledDrag := false  // waited for a seemingly fruitless drag before judging it
	var last *ui.Node     // where the element was in the previous read
	var stillSince time.Time
	var priorRows collectionSpan
	var shorterList string
	var shelfID string
	var shelfWindow int
	var shelfBounds ui.Rect
	// Drags that brought nothing new into view. A paging list springs back from a drag that
	// does not fling (when it is shorter than a page), so after two the swipes fling, as
	// the classic swipes do.
	seen := map[string]bool{}
	fruitless, fling := 0, false
	swipes, scrollStart := 0, time.Now()
	nudged := false            // a fully visible element flush with an edge was brought in once
	nudgePending := false      // ...and the drag that brings it in is still to come
	tried := map[string]bool{} // vertical lists dragged so far (listKey)
	// placeVertical moves up-and-down drags (and the fallback flings) into box: from 3/4 of
	// its visible height to near its top (or the reverse), at its middle.
	placeVertical := func(box *ui.Node) {
		b := box.Bounds.Intersect(screen)
		x1, x2, fx1, fy1 = b.CX(), b.CX(), b.CX(), b.CY()
		if c.Direction == "DOWN" {
			y1, y2 = b.T+b.H()*3/4, b.T+b.H()/10
		} else {
			y1, y2 = b.T+b.H()/4, b.T+b.H()*9/10
		}
		if (d.Platform() == "android" && box.Coll != nil && box.Coll.Rows > 1) ||
			(d.Platform() == "ios" && box.Class == "CollectionView") {
			// Two drags can arrive in one collection update. Keep their combined
			// travel smaller than the viewport, even if row indices are unavailable.
			stride := max(1, b.H()*45/100)
			y1 = y2 + clamp(y1-y2, -stride, stride)
		}
	}
	placeSideways := func(b ui.Rect) {
		y1, y2, fx1, fy1 = b.CY(), b.CY(), b.CX(), b.CY()
		if c.Direction == "RIGHT" {
			x1, x2 = b.L+b.W()*85/100, b.L+b.W()*15/100
		} else {
			x1, x2 = b.L+b.W()*15/100, b.L+b.W()*85/100
		}
	}
	flung := false // the last swipe was a fling
	broughtIn := 0 // drags that brought a sideways list into view first
	for {
		s, err := r.read(ctx)
		if err != nil {
			return err
		}
		n, _, err := r.match(ctx, s, sel)
		if err != nil {
			return &invalidField{msg: err.Error()}
		}
		if shelfID != "" {
			var bounds ui.Rect
			matches := 0
			for _, candidate := range s.Nodes {
				if candidate.ID == shelfID && s.Windows[candidate.Window].ID == shelfWindow {
					matches++
					bounds = candidate.Bounds.Intersect(screen)
				}
			}
			if (matches != 1 || bounds.Empty()) && n == nil && time.Now().Before(deadline) {
				if err := ctx.Err(); err != nil {
					return err
				}
				r.waitAny(ctx, events, s.Seq, minTime(deadline, time.Now().Add(restlessAfter)))
				r.invalidate()
				continue
			}
			if matches == 1 && !bounds.Empty() && bounds != shelfBounds {
				shelfBounds = bounds
				placeSideways(bounds)
				dragged, stalled = time.Time{}, time.Time{}
				fruitless, fling, flung = 0, false, false
				seen = map[string]bool{}
				r.tracef("the parent moved the shelf; aiming at y=%d", y1)
			}
		}
		// A delayed RecyclerView update can combine two drags in one tree read.
		// Its row indices tell us when we skipped an entire interval. Inspect
		// that interval before advancing, using smaller drags for this list.
		if n == nil && vertical && priorRows.key != "" && time.Now().Before(deadline) {
			box := scrollContainer(s, x1, y1)
			current := visibleCollectionRows(s, box)
			skipped := current.key == priorRows.key && ((c.Direction == "DOWN" && current.lo > priorRows.hi+1) || (c.Direction == "UP" && current.hi+1 < priorRows.lo))
			if skipped {
				// Let the previous correction appear before issuing another one.
				if !dragged.IsZero() && listSignature(s, x1, y1, true) == before && time.Since(dragged) < 500*time.Millisecond {
					r.waitAny(ctx, events, s.Seq, dragged.Add(min(500*time.Millisecond, deadline.Sub(dragged))))
					continue
				}
				b := box.Bounds.Intersect(screen)
				step := max(1, b.H()/3)
				if c.Direction == "UP" {
					step = -step
				}
				shorterList = current.key
				r.tracef("collection advanced from rows %d–%d to %d–%d; dragging back to inspect the gap", priorRows.lo, priorRows.hi, current.lo, current.hi)
				before = listSignature(s, x1, y1, true)
				r.invalidate()
				dragged = time.Now()
				settledDrag = false
				swipes++
				if _, err := d.Swipe(ctx, b.CX(), b.CY(), b.CX(), b.CY()+step, dur, drag); err != nil {
					return err
				}
				fling, flung, fruitless = false, false, 0
				continue
			}
		}
		vis := 0.0
		if n != nil {
			vis = match.VisibleShare(n.Bounds, n.ClipRect(screen))
		}
		centering := c.CenterElement && vis > 0.1 && centerTries <= 4
		if need >= 1 && !nudged && n != nil && vis >= need && d.Platform() == "android" && flush(n.Bounds, n.ClipRect(screen), vertical) {
			// Android reports bounds already cut to what shows: a row flush with the edge of
			// its list (or the screen) may be partly hidden there. Bring it in once.
			r.tracef("it sits flush with the edge of its list: it may be cut off there; bringing it in")
			nudged, nudgePending = true, true
		}
		centering = n != nil && (centering || nudgePending)
		done := n != nil && !centering && vis >= need
		// Partly in view, all of it asked for, and it can show whole: the next drag brings it in
		// (below), never a fling or a full drag, which can carry it past.
		partial := n != nil && !centering && vis > 0 && vis < need && fits(n.Bounds, n.ClipRect(screen), vertical)
		if centering && centered(n.Bounds, screen, c.Direction) {
			done, centering = true, false
		} else if centering && !nudgePending && vis >= 1 && d.Platform() == "android" &&
			centerScrollLimited(n, scrollContainer(s, x1, y1), screen, vertical) {
			r.tracef("the element is fully visible and its list cannot move farther toward the center")
			done, centering = true, false
		}
		unmoved := !dragged.IsZero() && listSignature(s, x1, y1, vertical) == before
		if unmoved && time.Since(dragged) < 500*time.Millisecond && time.Now().Before(deadline) {
			// The drag has not shown yet (the app is a frame or two behind): another one now
			// could carry the element past the screen.
			r.waitAny(ctx, events, s.Seq, dragged.Add(500*time.Millisecond))
			continue
		}
		if !dragged.IsZero() && n != nil && (done || centering || vis > 0) {
			// It shows after a drag. The content stops at the lift, but an app can take a frame
			// or two to move it, a list end bounces back, a paging list snaps: it counts once two
			// reads in a row agree on where it is (or the UI settles, after a second).
			checkCoast := flung && d.Platform() == "ios"
			same := last != nil && last.Bounds == n.Bounds
			if checkCoast && same && last.PreciseBounds != nil && n.PreciseBounds != nil {
				same = *last.PreciseBounds == *n.PreciseBounds
			}
			if !same {
				stillSince = time.Now()
			}
			last = n
			if checkCoast && time.Since(stillSince) < restSpanAfterAction && time.Since(dragged) < restWait &&
				time.Now().Before(deadline) {
				// A fling can coast by fractions of a point without a new accessibility
				// generation. A repeated cached tree doesn't show that it has stopped.
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(restGap):
				}
				r.invalidate()
				continue
			}
			if !same {
				if !checkCoast && time.Since(dragged) < time.Second {
					r.waitAny(ctx, events, s.Seq, time.Now().Add(80*time.Millisecond))
					continue
				}
				d.WaitSettled(ctx, 2*time.Second)
			}
			dragged = time.Time{}
			if checkCoast {
				r.invalidate()
			}
			continue
		}
		last = n
		if done {
			if swipes > 0 {
				r.tracef("in view after %d swipes, %s: %s", swipes, since(scrollStart), nodeDesc(n))
			}
			return nil
		}
		if unmoved && fruitless == 0 {
			// Not there, and the drag moved nothing: a list still loading, or at its end. The
			// next drag waits up to 2 s for the screen to change (once: after that, the
			// swipes fling, below).
			if stalled.IsZero() {
				stalled = dragged
			}
			if dl := stalled.Add(2 * time.Second); time.Now().Before(dl) && time.Now().Before(deadline) {
				r.tracef("the drag moved nothing (a list loading, or at its end): waiting up to 2 s for a change")
				r.waitAny(ctx, events, s.Seq, dl)
				continue
			}
		}
		stalled = time.Time{}
		if !time.Now().Before(deadline) {
			r.tracef("gave up after %d swipes, %s (fling mode: %v)", swipes, since(scrollStart), fling)
			return failf("No visible element found: %s", sel.Describe())
		}
		if fling && flung && n == nil {
			// The last fling brought new things into view: the list scrolls (it is not a
			// paging list springing back), so go back to drags, which cannot carry the element
			// past the screen. A paging list springs back again, and flings again.
			if newTexts(s, x1, y1, seen) > 0 {
				r.tracef("the fling brought new things into view: dragging again")
				fling, fruitless = false, 0
			}
		}
		flung = false
		if !fling && !dragged.IsZero() && n == nil {
			fresh := newTexts(s, x1, y1, seen)
			if fresh == 0 && !settledDrag {
				// Nothing new yet: the app may be a frame or two behind the drag (a screen
				// that never stops moving reads as changed while the list has not moved).
				// Judge it on the settled screen, not this read: two fruitless drags switch
				// to flinging for good, and a fling can carry the element past the screen.
				d.WaitSettled(ctx, time.Second)
				r.invalidate()
				settledDrag = true
				// The settled read can reveal a skipped collection interval too.
				continue
			}
			if fresh == 0 {
				fruitless++
			} else {
				fruitless = 0
			}
			if fruitless >= 2 && vertical {
				// This list shows nothing new: another list on screen may hold the element
				// (the other pane of a two-pane layout). Try it before flinging this one.
				markTried(s, x1, y1, tried)
				if box := otherList(s, tried); box != nil {
					tried[listKey(box)] = true
					placeVertical(box)
					fruitless = 0
					r.tracef("2 drags brought nothing new here: trying the other list, %s", nodeDesc(box))
				}
			}
			if fruitless >= 2 && !fling {
				r.tracef("2 drags in a row brought nothing new into view (a paging list springs back): flinging from now on")
			}
			fling = fruitless >= 2
		} else if dragged.IsZero() {
			newTexts(s, x1, y1, seen)
		}
		if fling && !partial {
			// A classic swipe: it flings, and settles before the next look.
			r.invalidate()
			swipes++
			res, err := d.Swipe(ctx, fx1, fy1, x2, y2, dur, driver.ActionOpts{NoChangeMs: 300})
			if err != nil {
				return err
			}
			flung = true
			r.tracef("fling %d: (%d, %d) -> (%d, %d): %s", swipes, fx1, fy1, x2, y2, actionDesc(res))
			// A fling coasts on: like a drag, the element counts once two reads agree on
			// where it is (it can be found while the list still moves, and then carried off).
			dragged, last, stillSince = time.Now(), nil, time.Time{}
			before = 0
			continue
		}
		if vertical && !placed {
			// Up and down: across the list the element is listed in, when that is not the
			// one at the middle of the screen (a two-pane layout: the middle is the other
			// pane); else from the middle, as a person would.
			listed := listedIn(s, n)
			box := ownList(s, listed, x1, y1)
			if listed == nil && swipes == 0 {
				var lists []*ui.Node
				for _, candidate := range s.Nodes {
					if visibleVerticalList(candidate, screen) {
						lists = append(lists, candidate)
					}
				}
				if similar := listWithSimilarLabels(lists, sel.Text); similar != nil && !similar.Bounds.Contains(x1, y1) {
					box = similar
				}
			}
			if box != nil {
				placeVertical(box)
				placed = true
				tried[listKey(box)] = true
				r.tracef("dragging the most likely list for the element: %s", nodeDesc(box))
			} else {
				y1, placed = dragStart(s, d.Platform(), x1, y1, c.Direction)
				markTried(s, x1, y1, tried)
			}
		} else if !vertical && !placed {
			// Sideways: across the sideways list the element is in, or the one on screen when
			// the middle of the screen has none (a carousel above the middle): a swipe there
			// would move nothing.
			placed = true
			listed := listedIn(s, n)
			if box := sidewaysList(s, listed, w/2, h/2, sel.Text); box != nil {
				if shown := box.Bounds.Intersect(screen); shown.H()*10 < box.Bounds.H()*6 && broughtIn < 2 {
					// Mostly off screen (a shelf at the bottom edge): bring it to the middle
					// first, dragging the page, so the sideways drags run across it, not
					// along a sliver at the screen's edge.
					dy := clamp(box.Bounds.CY()-h/2, -h*4/10, h*4/10)
					r.tracef("the list to drag sideways is mostly off screen: bringing it in first")
					r.invalidate()
					broughtIn++
					if _, err := d.Swipe(ctx, w/2, h/2+dy/2, w/2, h/2-dy/2, dur, drag); err != nil {
						return err
					}
					d.WaitSettled(ctx, time.Second)
					placed = false
					continue
				}
				placeSideways(box.Bounds.Intersect(screen))
				if in != nil && box.ID != "" {
					shelfID, shelfWindow = box.ID, s.Windows[box.Window].ID
					shelfBounds = box.Bounds.Intersect(screen)
				}
			}
		}
		tx, ty := x2, y2
		if centering {
			// Just far enough to bring it to the middle, at most a whole drag.
			centerTries++
			if vertical {
				stride := abs(y2 - y1)
				ty = y1 + clamp(h/2-n.Bounds.CY(), -stride, stride)
			} else {
				stride := abs(x2 - x1)
				tx = x1 + clamp(w/2-n.Bounds.CX(), -stride, stride)
			}
		} else if partial {
			// Partly in view, and all of it is asked for: just far enough to bring it to the middle
			// of its list. An element nearly as tall as the list is whole only within a span
			// shorter than a drag, and full drags step over that span (a grid of big cells).
			clip := n.ClipRect(screen)
			if vertical {
				stride := abs(y2 - y1)
				ty = y1 + clamp(clip.CY()-n.Bounds.CY(), -stride, stride)
			} else {
				stride := abs(x2 - x1)
				tx = x1 + clamp(clip.CX()-n.Bounds.CX(), -stride, stride)
			}
			r.tracef("it shows in part: dragging it to the middle of its list")
		}
		if vertical && d.Platform() == "android" {
			box := scrollContainer(s, x1, y1)
			priorRows = visibleCollectionRows(s, box)
			if shorterList != "" && shorterList == priorRows.key && !centering && !partial {
				stride := max(1, box.Bounds.Intersect(screen).H()/3)
				ty = y1 + clamp(ty-y1, -stride, stride)
			}
		}
		before = listSignature(s, x1, y1, vertical)
		r.invalidate()
		dragged = time.Now()
		settledDrag = false
		nudgePending = false
		swipes++
		if swipes == 1 && !vertical {
			r.tracef("dragging sideways at y=%d (x %d -> %d)", y1, x1, tx)
		}
		if _, err := d.Swipe(ctx, x1, y1, tx, ty, dur, drag); err != nil {
			return err
		}
		if n != nil {
			r.tracef("drag %d: (%d, %d) -> (%d, %d); target listed but %.0f%% visible", swipes, x1, y1, tx, ty, vis*100)
		} else {
			r.tracef("drag %d: (%d, %d) -> (%d, %d); target not on screen yet, %d texts seen", swipes, x1, y1, tx, ty, len(seen))
		}
	}
}
