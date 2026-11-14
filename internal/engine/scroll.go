package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
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
	placed := false        // the drag's start moved into the list (iOS, up and down)
	var dragged time.Time  // when the last drag began; zero once the screen is known to be still
	var dragDone time.Time // when the last drag returned (zero while it is being sent)
	// waitFrom is when the waits after a drag that moved nothing are timed from: its start,
	// or in CI mode its return (issue #111: a drag that itself took longer than the wait, on a
	// simulator still starting up, got no wait at all).
	waitFrom := func() time.Time {
		if ci.On() && dragDone.After(dragged) {
			// Only this drag's return: a fling sets dragged but not dragDone.
			return dragDone
		}
		return dragged
	}
	stallSaid := false    // said that a slow drag used up the wait for a change
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
	// Swipes that moved the screen at all, told by the screen they started from (issue #109: a
	// simulator still starting up can drop a swipe's touches, which looks like the end of a list).
	moved, unconfirmed := 0, false
	var sentFrom uint64
	// Swipes the iOS agent reported as never confirmed by the app (issue #111): all of them,
	// those in a row up to the last, and whether the last one was.
	neverConfirmed, neverConfirmedRun, lastNeverConfirmed := 0, 0, false
	// confirmed notes what a swipe's result says of its touches reaching the app.
	confirmed := func(res driver.ActionResult) {
		lastNeverConfirmed = res.Unconfirmed
		if !res.Unconfirmed {
			neverConfirmedRun = 0
			return
		}
		neverConfirmed++
		neverConfirmedRun++
		r.tracef("the app never confirmed swipe %d's touches (testmanagerd timed out waiting for them): they may not have reached it", swipes)
	}
	sending := func(s *ui.Screen) {
		if unconfirmed {
			r.tracef("swipe %d changed nothing on screen (a list at its end, or touches that did not reach the app)", swipes)
		}
		sentFrom, unconfirmed = screenSignature(s, vertical), true
		swipes++
	}
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
	// noRoom: a keyboard leaves no room for a swipe that is not a tap (issue #175): send
	// nothing, and wait a while for the screen to change (the keyboard may go).
	noRoomSaid := false
	noRoom := func(s *ui.Screen) {
		if !noRoomSaid {
			r.tracef("no room above the keyboard for a swipe that is not a tap: sending none, waiting for the screen to change")
			noRoomSaid = true
		}
		r.waitAny(ctx, events, s.Seq, minTime(deadline, time.Now().Add(500*time.Millisecond)))
		r.invalidate()
	}
	flung := false           // the last swipe was a fling
	leftWhy := ""            // scrollStop's reason on the last read, to be confirmed by the next
	broughtIn := 0           // drags that brought a sideways list into view first
	var bringInFrom ui.Rect  // the sideways list's bounds before the last of those drags
	shelfHiddenSaid := false // said that the tracked shelf is all under the keyboard
	for {
		s, err := r.read(ctx)
		if err != nil {
			return err
		}
		if unconfirmed && screenSignature(s, vertical) != sentFrom {
			moved, unconfirmed = moved+1, false
		}
		n, _, err := r.match(ctx, s, sel)
		if err != nil {
			return &invalidField{msg: err.Error()}
		}
		if n == nil && r.scrollStop != nil {
			// Not in view, and maybe not the screen the scroll began on (bringBack): stop when
			// a fresh read, before any further drag, says so too.
			if why := r.scrollStop(s); why == "" {
				leftWhy = ""
			} else if why == leftWhy {
				r.tracef("%s after %d swipes: not scrolling this screen", why, swipes)
				return &screenLeft{why: why, s: s}
			} else {
				leftWhy = why
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(restGap):
				}
				r.invalidate()
				continue
			}
		}
		// What shows of the screen: on Android, above a keyboard along its bottom (issue #175).
		view := screen
		if d.Platform() == "android" {
			view = clearOfKeyboard(s, screen)
		}
		if shelfID != "" {
			var bounds ui.Rect
			matches := 0
			for _, candidate := range s.Nodes {
				if candidate.ID == shelfID && s.Windows[candidate.Window].ID == shelfWindow {
					matches++
					// Only what shows: a shelf the keyboard hides cannot be dragged (issue #193).
					bounds = candidate.Bounds.Intersect(view)
				}
			}
			if (matches != 1 || bounds.Empty()) && n == nil && time.Now().Before(deadline) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if matches == 1 && view != screen && !shelfHiddenSaid {
					r.tracef("the shelf is all under the keyboard: waiting for the screen to change")
					shelfHiddenSaid = true
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
				b := box.Bounds.Intersect(screen)
				step := max(1, b.H()/3)
				if c.Direction == "UP" {
					step = -step
				}
				if _, _, _, _, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, b.CX(), b.CY(), b.CX(), b.CY()+step); !ok {
					// No room above the keyboard for a drag back that is not a tap: go on.
					r.tracef("collection advanced from rows %d–%d to %d–%d; no room above the keyboard to drag back", priorRows.lo, priorRows.hi, current.lo, current.hi)
					skipped, priorRows = false, collectionSpan{}
				}
			}
			if skipped {
				// Let the previous correction appear before issuing another one.
				if from := waitFrom(); !dragged.IsZero() && listSignature(s, x1, y1, true) == before && time.Since(from) < 500*time.Millisecond {
					r.waitAny(ctx, events, s.Seq, from.Add(min(500*time.Millisecond, deadline.Sub(from))))
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
				sending(s)
				cx, cy1, _, cy2, _ := clearOfKeyboardSwipe(s, d.Platform(), screen, b.CX(), b.CY(), b.CX(), b.CY()+step)
				r.mark("swipe", [2]int{cx, cy1}, [2]int{cx, cy2})
				dragDone = time.Time{}
				res, err := d.Swipe(ctx, cx, cy1, cx, cy2, dur, drag)
				if err != nil {
					return err
				}
				dragDone = time.Now()
				confirmed(res)
				fling, flung, fruitless = false, false, 0
				continue
			}
		}
		vis := 0.0
		if n != nil {
			vis = match.VisibleShare(n.Bounds, n.ClipRect(screen))
		}
		centering := c.CenterElement && vis > 0.1 && centerTries <= 4
		if need >= 1 && !nudged && n != nil && vis >= need && d.Platform() == "android" && flush(n.Bounds, n.ClipRect(view), vertical) {
			// Android reports bounds already cut to what shows: a row flush with the edge of
			// its list (or the screen, or the keyboard's top) may be partly hidden there. Bring
			// it in once: one drag toward the middle of what shows above a keyboard, unless it
			// is near that middle already (issue #175: measured against the whole screen, the
			// keyboard counted as showing, and no drag came).
			r.tracef("it sits flush with the edge of its list: it may be cut off there; bringing it in")
			nudged, nudgePending = true, true
		}
		centering = n != nil && (centering || nudgePending)
		done := n != nil && !centering && vis >= need
		// Partly in view, all of it asked for, and it can show whole: the next drag brings it in
		// (below), never a fling or a full drag, which can carry it past.
		partial := n != nil && !centering && vis > 0 && vis < need && fits(n.Bounds, n.ClipRect(screen), vertical)
		if centering && centered(n.Bounds, view, c.Direction) {
			done, centering = true, false
		} else if centering && !nudgePending && vis >= 1 && d.Platform() == "android" &&
			centerScrollLimited(n, scrollContainer(s, x1, y1), view, vertical) {
			r.tracef("the element is fully visible and its list cannot move farther toward the center")
			done, centering = true, false
		}
		unmoved := !dragged.IsZero() && listSignature(s, x1, y1, vertical) == before
		if unmoved && time.Since(waitFrom()) < 500*time.Millisecond && time.Now().Before(deadline) {
			// The drag has not shown yet (the app is a frame or two behind): another one now
			// could carry the element past the screen.
			r.waitAny(ctx, events, s.Seq, waitFrom().Add(500*time.Millisecond))
			continue
		}
		if unmoved && lastNeverConfirmed && ci.On() && n == nil {
			// The agent said the app never confirmed the drag's touches, and the list moved
			// neither in the 5 s testmanagerd waited for them nor in the 500 ms since (above,
			// timed in CI mode from the drag's return): they did not reach it (issue #111, a
			// simulator still starting up). An app whose main thread was only blocked moves
			// the list with the queued touches once it runs, and is sent no second drag, which
			// would scroll it twice as far. Waiting longer for a change would only use up the
			// time, and the drag says nothing of the list: send it again now.
			r.tracef("swipe %d moved nothing and its touches were never confirmed: sending it again at once (CI mode)", swipes)
			dragged, stalled, lastNeverConfirmed, unmoved = time.Time{}, time.Time{}, false, false
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
				stalled = waitFrom()
				// Only where the wait is timed from the drag's start (outside CI mode).
				if took := dragDone.Sub(dragged); !stallSaid && stalled.Equal(dragged) && dragDone.After(dragged) && took >= time.Second && !time.Now().Before(stalled.Add(2*time.Second)) {
					// Issue #111: say so, rather than skip the wait without a word.
					r.tracef("the drag moved nothing, and took %.1f s to send, which used up the 2 s wait for a change", took.Seconds())
					stallSaid = true
				}
			}
			if dl := stalled.Add(2 * time.Second); time.Now().Before(dl) && time.Now().Before(deadline) {
				r.tracef("the drag moved nothing (a list loading, or at its end): waiting up to 2 s for a change")
				r.waitAny(ctx, events, s.Seq, dl)
				continue
			}
		}
		stalled = time.Time{}
		if !time.Now().Before(deadline) && r.stallGrace(&deadline, timeout, scrollStart) {
			r.invalidate()
			continue
		}
		if !time.Now().Before(deadline) {
			if unconfirmed {
				r.tracef("swipe %d changed nothing on screen (a list at its end, or touches that did not reach the app)", swipes)
			}
			r.tracef("gave up after %d swipes (%d moved the screen), %s (fling mode: %v)", swipes, moved, since(scrollStart), fling)
			if swipes == 0 {
				return failf("No visible element found: %s", sel.Describe())
			}
			noun, movers := "swipes", "none"
			if swipes == 1 {
				noun = "swipe"
			}
			if moved > 0 {
				movers = strconv.Itoa(moved)
			}
			if neverConfirmed > 0 {
				// Issue #111: the agent's word that touches may not have reached the app.
				return failf("No visible element found: %s (after %d %s, %s of which moved the screen; the app never confirmed the touches of %d)", sel.Describe(), swipes, noun, movers, neverConfirmed)
			}
			return failf("No visible element found: %s (after %d %s, %s of which moved the screen)", sel.Describe(), swipes, noun, movers)
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
				// Issue #111: not only a paging list. A list at its end, or drags whose touches
				// never reached the app, bring nothing new either.
				why := "a paging list springs back, a list at its end, or touches that did not reach the app"
				if neverConfirmedRun > 0 {
					why = "the app never confirmed the touches of the last one"
					if neverConfirmedRun > 1 {
						why = "the app never confirmed the touches of either"
					}
				}
				r.tracef("2 drags in a row brought nothing new into view (%s): flinging from now on", why)
			}
			fling = fruitless >= 2
		} else if dragged.IsZero() {
			newTexts(s, x1, y1, seen)
		}
		if fling && !partial {
			// A classic swipe: it flings, and settles before the next look.
			sx, sy, ex, ey, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, fx1, fy1, x2, y2)
			if !ok {
				noRoom(s)
				continue
			}
			r.invalidate()
			sending(s)
			r.mark("swipe", [2]int{sx, sy}, [2]int{ex, ey})
			res, err := d.Swipe(ctx, sx, sy, ex, ey, dur, driver.ActionOpts{NoChangeMs: 300})
			if err != nil {
				return err
			}
			confirmed(res)
			flung = true
			r.tracef("fling %d: (%d, %d) -> (%d, %d): %s", swipes, sx, sy, ex, ey, actionDesc(res))
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
				// What shows of it: above a keyboard, on Android. On a screen that does not
				// make room for the keyboard (adjustPan, adjustNothing, Compose without
				// imePadding) the shelf can be on screen but under the keyboard, with only a
				// strip of it above (issue #193). Up to two drags bring it in; with a keyboard,
				// which leaves less room per drag, up to four.
				bringIns := 2
				if view != screen {
					bringIns = 4
				}
				if broughtIn > 0 && broughtIn < bringIns && box.Bounds == bringInFrom {
					// The last drag to bring it in moved nothing: the page does not scroll (a
					// form under a keyboard it does not make room for). More such drags would
					// only use up the time: drag the list where it is.
					r.tracef("bring-in drag %d moved nothing: dragging the list where it is", broughtIn)
					broughtIn = bringIns
				}
				if shown := box.Bounds.Intersect(view); shown.H()*10 < box.Bounds.H()*6 && broughtIn < bringIns {
					// Mostly off screen or under the keyboard (a shelf at the bottom edge):
					// bring it to the middle of what shows first, dragging the page, so the
					// sideways drags run across it, not along a sliver at the edge.
					dy := clamp(box.Bounds.CY()-view.CY(), -view.H()*4/10, view.H()*4/10)
					broughtIn++
					bringInFrom = box.Bounds
					if bx, by1, _, by2, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, w/2, view.CY()+dy/2, w/2, view.CY()-dy/2); ok {
						if view != screen {
							r.tracef("the list to drag sideways is mostly off screen or under the keyboard: bringing it in above the keyboard first")
						} else {
							r.tracef("the list to drag sideways is mostly off screen: bringing it in first")
						}
						r.invalidate()
						r.mark("swipe", [2]int{bx, by1}, [2]int{bx, by2})
						if _, err := d.Swipe(ctx, bx, by1, bx, by2, dur, drag); err != nil {
							return err
						}
						r.tracef("bring-in drag %d: (%d, %d) -> (%d, %d)", broughtIn, bx, by1, bx, by2)
						d.WaitSettled(ctx, time.Second)
						placed = false
						continue
					}
					r.tracef("the list to drag sideways is mostly off screen, and the keyboard leaves no room to bring it in")
					broughtIn = bringIns
				}
				shown := box.Bounds.Intersect(view)
				if shown.Empty() && view != screen {
					// All of it under the keyboard, and the page did not bring it up: a
					// sideways swipe above the keyboard would land on the page, and move
					// nothing (issue #193). Send none; wait for the screen to change.
					placed = false
					noRoom(s)
					continue
				}
				placeSideways(shown)
				if in != nil && box.ID != "" {
					shelfID, shelfWindow = box.ID, s.Windows[box.Window].ID
					shelfBounds = shown
				}
			}
		}
		tx, ty := x2, y2
		if centering {
			// Just far enough to bring it to the middle of what shows (above a keyboard), at
			// most a whole drag.
			centerTries++
			if vertical {
				stride := abs(y2 - y1)
				ty = y1 + clamp(view.CY()-n.Bounds.CY(), -stride, stride)
			} else {
				stride := abs(x2 - x1)
				tx = x1 + clamp(view.CX()-n.Bounds.CX(), -stride, stride)
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
		sx, sy, tx, ty, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, x1, y1, tx, ty)
		if centering && d.Platform() == "android" && abs(tx-sx)+abs(ty-sy) < minDrag(screen) {
			// Bringing it in (or centering it) would be a drag shorter than twice the touch
			// slop, which the app takes for a tap: an element that fills its list along the
			// drag is already as far in as it goes, or a keyboard leaves no room (issue #175).
			r.tracef("bringing it in would be a %d px drag, too short to move its list: leaving it there", abs(tx-sx)+abs(ty-sy))
			nudgePending, centerTries = false, 5
			if vis >= need {
				if swipes > 0 {
					r.tracef("in view after %d swipes, %s: %s", swipes, since(scrollStart), nodeDesc(n))
				}
				return nil
			}
			continue
		}
		if !ok {
			noRoom(s)
			continue
		}
		before = listSignature(s, x1, y1, vertical)
		r.invalidate()
		dragged = time.Now()
		settledDrag = false
		nudgePending = false
		sending(s)
		if swipes == 1 && !vertical {
			r.tracef("dragging sideways at y=%d (x %d -> %d)", sy, sx, tx)
		}
		r.mark("swipe", [2]int{sx, sy}, [2]int{tx, ty})
		dragDone = time.Time{}
		res, err := d.Swipe(ctx, sx, sy, tx, ty, dur, drag)
		if err != nil {
			return err
		}
		dragDone = time.Now()
		if n != nil {
			r.tracef("drag %d: (%d, %d) -> (%d, %d); target listed but %.0f%% visible", swipes, sx, sy, tx, ty, vis*100)
		} else {
			r.tracef("drag %d: (%d, %d) -> (%d, %d); target not on screen yet, %d texts seen", swipes, sx, sy, tx, ty, len(seen))
		}
		confirmed(res)
	}
}
