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

func (r *Runner) scrollTo(ctx context.Context, c *flow.Command, sel *match.Selector) error {
	d := r.Drv
	var err error
	var in *match.Selector
	if c.In != nil {
		if in, err = r.resolveSelector(c.In); err != nil {
			return err
		}
	}
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
	x1, y1 := w/2, h/2
	var x2, y2 int
	switch c.Direction {
	case "DOWN":
		x2, y2 = w/2, h/10
	case "UP":
		x2, y2 = w/2, h*9/10
	case "RIGHT":
		x1, x2, y2 = w*85/100, w*15/100, h/2
	default:
		x1, x2, y2 = w*15/100, w*85/100, h/2
	}
	fx1, fy1 := w/2, h/2

	drag := driver.ActionOpts{NoSettle: true, Drag: true}
	events, stop := d.Events()
	defer stop()
	readyWatch := r.watchChanges(true)
	var readyAt readyState
	ready := false
	defer func() {
		if !ready {
			readyWatch.Stop()
		}
	}()
	deadline := time.Now().Add(timeout)
	r.verdictRead()
	centerTries := 0
	var centerFrom ui.Rect
	centerSent := false
	centerOver := false
	centerResent := false
	placed := false
	var dragged time.Time
	var dragDone time.Time
	waitFrom := func() time.Time {
		if ci.On() && dragDone.After(dragged) {
			return dragDone
		}
		return dragged
	}
	stallSaid := false
	var before uint64
	var stalled time.Time
	settledDrag := false
	var last *ui.Node
	var stillSince time.Time
	var priorRows collectionSpan
	var shorterList string
	var shelfID string
	var shelfWindow int
	var shelfBounds ui.Rect
	seen := map[string]bool{}
	fruitless, fling := 0, false
	endWaited := false
	swipes, scrollStart := 0, time.Now()
	moved, unconfirmed := 0, false
	var sentFrom uint64
	neverConfirmed, neverConfirmedRun := 0, 0
	var deliveryToken int64
	confirmed := func(res driver.ActionResult) {
		deliveryToken = res.DeliveryToken
		if !res.Unconfirmed {
			neverConfirmedRun = 0
			return
		}
		neverConfirmed++
		neverConfirmedRun++
		r.tracef("swipe %d: neither the app nor the system reported its touches completed: whether it reached the app is unknown", swipes)
	}
	againRun := 0
	sendAgainIf := func(still func(*ui.Screen) (bool, error)) (bool, error) {
		token := deliveryToken
		deliveryToken = 0
		if token == 0 {
			return false, nil
		}
		if againRun >= 2 {
			r.tracef("swipe %d moved nothing: 2 swipes in a row were sent again already, not asking about this one", swipes)
			return false, nil
		}
		if !r.swipeUndelivered(ctx, token, swipes) {
			againRun = 0
			return false, nil
		}
		r.invalidate()
		s, err := r.read(ctx)
		if err != nil {
			return false, err
		}
		if ok, err := still(s); err != nil || !ok {
			if err == nil {
				r.tracef("the list moved while the app was asked about swipe %d: not sending it again", swipes)
			}
			againRun = 0
			return false, err
		}
		againRun++
		r.tracef("swipe %d: sending it again at once (CI mode)", swipes)
		return true, nil
	}
	sendAgain := func(from uint64) (bool, error) {
		return sendAgainIf(func(s *ui.Screen) (bool, error) {
			return listSignature(s, x1, y1, vertical) == from, nil
		})
	}
	sending := func(s *ui.Screen) {
		if unconfirmed {
			r.tracef("swipe %d changed nothing on screen (a list at its end, or touches that did not reach the app)", swipes)
		}
		sentFrom, unconfirmed = screenSignature(s, vertical), true
		swipes++
	}
	nudged := false
	nudgePending := false
	tried := map[string]bool{}
	ownKey := ""
	placeVertical := func(box *ui.Node) {
		ownKey = listKey(box)
		b := box.Bounds.Intersect(screen)
		x1, x2, fx1, fy1 = b.CX(), b.CX(), b.CX(), b.CY()
		if c.Direction == "DOWN" {
			y1, y2 = b.T+b.H()*3/4, b.T+b.H()/10
		} else {
			y1, y2 = b.T+b.H()/4, b.T+b.H()*9/10
		}
		if (d.Platform() == "android" && box.Coll != nil && box.Coll.Rows > 1) ||
			(d.Platform() == "ios" && box.Class == "CollectionView") {
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
	noRoomSaid := false
	noRoom := func(s *ui.Screen) {
		if !noRoomSaid {
			r.tracef("no room above the keyboard for a swipe that is not a tap: sending none, waiting for the screen to change")
			noRoomSaid = true
		}
		r.waitAny(ctx, events, s.Seq, minTime(deadline, time.Now().Add(500*time.Millisecond)))
		r.invalidate()
	}
	flung := false
	leftWhy := ""
	broughtIn := 0
	var bringInFrom ui.Rect
	shelfHiddenSaid := false
	var guard *dragGuard
	unguarded := false
	shortBy := 0
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
				fruitless, fling, flung, endWaited = 0, false, false, false
				seen = map[string]bool{}
				r.tracef("the parent moved the shelf; aiming at y=%d", y1)
			}
		}
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
					r.tracef("collection advanced from rows %d–%d to %d–%d; no room above the keyboard to drag back", priorRows.lo, priorRows.hi, current.lo, current.hi)
					skipped, priorRows = false, collectionSpan{}
				}
			}
			if skipped {
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
				centerSent = false
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
				fling, flung, fruitless, endWaited = false, false, 0, false
				guard = nil
				continue
			}
		}
		if guard != nil && vertical {
			g := guard
			moved := listSignature(s, x1, y1, true) != before
			shift, landed := 0, false
			if moved {
				shift, landed = g.confirmed(s)
			}
			back := false
			switch {
			case n != nil:
				guard = nil
			case !moved && !g.back:
			case !moved && time.Since(waitFrom()) < 500*time.Millisecond && time.Now().Before(deadline):
				r.waitAny(ctx, events, s.Seq, waitFrom().Add(500*time.Millisecond))
				continue
			case landed:
				guard = nil
				if shift < g.travel {
					shortBy = g.travel - shift
				}
			case moved && !g.waited && time.Now().Before(deadline):
				g.waited = true
				d.WaitSettled(ctx, time.Second)
				r.invalidate()
				continue
			default:
				guard, back = nil, true
				unguarded = true
			}
			if back {
				what := "no two rows seen before the long drag show where it should have put them"
				if g.back {
					what = "the drag back moved nothing"
					if moved {
						what = "the drag back did not land as one"
					}
				}
				rm, ok := listRoom(s, view, x1, y1, ownKey)
				var gb *dragGuard
				if ok {
					gb = g.dragBack(s, rm)
				}
				switch {
				case g.back && g.tries >= 3:
					r.tracef("%s, after %d drags back: going on, dragging shorter", what, g.tries)
				case gb == nil || !time.Now().Before(deadline):
					r.tracef("%s, and no room to drag back: going on, dragging shorter", what)
				default:
					if bx, by1, _, by2, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, gb.x, gb.sy, gb.x, gb.ty); ok {
						r.tracef("%s: dragging back %d px (%d), and shorter from now on", what, gb.travel, gb.tries)
						before = listSignature(s, x1, y1, true)
						centerSent = false
						r.invalidate()
						dragged = time.Now()
						settledDrag = false
						sending(s)
						r.mark("swipe", [2]int{bx, by1}, [2]int{bx, by2})
						dragDone = time.Time{}
						res, err := d.Swipe(ctx, bx, by1, bx, by2, dur, drag)
						if err != nil {
							return err
						}
						dragDone = time.Now()
						confirmed(res)
						fling, flung, fruitless, endWaited = false, false, 0, false
						gb.sy, gb.ty, gb.travel = by1, by2, abs(by2-by1)
						guard = gb
						continue
					}
					r.tracef("%s, and the keyboard leaves no room to drag back: going on, dragging shorter", what)
				}
			}
		}
		vis := 0.0
		if n != nil {
			vis = match.VisibleShare(n.Bounds, n.ClipRect(screen))
		}
		if n != nil && !sameSpot(n.Bounds, centerFrom) {
			if centerOver {
				r.tracef("the element moved after the centering stopped: centering it again")
			}
			centerOver = false
			if centerSent {
				centerResent = false
			}
		}
		centering := c.CenterElement && !centerOver && vis > 0.1 && centerTries <= 4
		if need >= 1 && !nudged && n != nil && vis >= need && d.Platform() == "android" && flush(n.Bounds, n.ClipRect(view), vertical) {
			r.tracef("it sits flush with the edge of its list: it may be cut off there; bringing it in")
			nudged, nudgePending = true, true
		}
		centering = n != nil && (centering || nudgePending)
		done := n != nil && !centering && vis >= need
		partial := n != nil && !centering && vis > 0 && vis < need && fits(n.Bounds, n.ClipRect(screen), vertical)
		if centering && centered(n.Bounds, view, c.Direction) {
			done, centering = true, false
		} else if centering && !nudgePending && vis >= 1 && d.Platform() == "android" &&
			centerScrollLimited(n, scrollContainer(s, x1, y1), view, vertical) {
			r.tracef("the element is fully visible and its list cannot move farther toward the center")
			done, centering, centerOver, centerFrom = true, false, true, n.Bounds
		} else if centering && !nudgePending && vis >= 1 && centerSent && sameSpot(n.Bounds, centerFrom) {
			from := dragged
			if dragDone.After(from) {
				from = dragDone
			}
			if !dragged.IsZero() && time.Since(from) < 500*time.Millisecond && time.Now().Before(deadline) {
				r.waitAny(ctx, events, s.Seq, from.Add(500*time.Millisecond))
				continue
			}
			again := false
			if !centerResent {
				var err error
				again, err = sendAgainIf(func(s *ui.Screen) (bool, error) {
					m, _, err := r.match(ctx, s, sel)
					if err != nil {
						return false, &invalidField{msg: err.Error()}
					}
					return m != nil && sameSpot(m.Bounds, centerFrom), nil
				})
				if err != nil {
					return err
				}
			}
			deliveryToken = 0
			if again {
				centerResent = true
				dragged, stalled, unconfirmed, centerSent = time.Time{}, time.Time{}, false, false
				continue
			}
			r.tracef("the element is fully visible and the centering drag did not move it: its list cannot move farther toward the center")
			done, centering, centerOver, centerFrom = true, false, true, n.Bounds
		}
		unmoved := !dragged.IsZero() && listSignature(s, x1, y1, vertical) == before
		if unmoved && time.Since(waitFrom()) < 500*time.Millisecond && time.Now().Before(deadline) {
			r.waitAny(ctx, events, s.Seq, waitFrom().Add(500*time.Millisecond))
			continue
		}
		if unmoved && n == nil && deliveryToken != 0 {
			again, err := sendAgain(before)
			if err != nil {
				return err
			}
			if again {
				dragged, stalled, unmoved, unconfirmed = time.Time{}, time.Time{}, false, false
			}
		} else if !dragged.IsZero() && before != 0 && !unmoved {
			againRun = 0
		}
		if !dragged.IsZero() && n != nil && (done || centering || vis > 0) {
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
		if unmoved && !endWaited {
			if stalled.IsZero() {
				stalled = waitFrom()
				if took := dragDone.Sub(dragged); !stallSaid && stalled.Equal(dragged) && dragDone.After(dragged) && took >= time.Second && !time.Now().Before(stalled.Add(2*time.Second)) {
					r.tracef("the drag moved nothing, and took %.1f s to send, which used up the 2 s wait for a change", took.Seconds())
					stallSaid = true
				}
			}
			if dl := stalled.Add(2 * time.Second); time.Now().Before(dl) && time.Now().Before(deadline) {
				r.tracef("the drag moved nothing (a list loading, or at its end): waiting up to 2 s for a change")
				r.waitAny(ctx, events, s.Seq, dl)
				continue
			}
			endWaited = true
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
				return failf("No visible element found: %s (after %d %s, %s of which moved the screen; whether %d reached the app is unknown)", sel.Describe(), swipes, noun, movers, neverConfirmed)
			}
			return failf("No visible element found: %s (after %d %s, %s of which moved the screen)", sel.Describe(), swipes, noun, movers)
		}
		if fling && flung && n == nil {
			if newTexts(s, x1, y1, seen) > 0 {
				r.tracef("the fling brought new things into view: dragging again")
				fling, fruitless, endWaited = false, 0, false
			}
		}
		flung = false
		if !fling && !dragged.IsZero() && n == nil {
			fresh := newTexts(s, x1, y1, seen)
			if fresh == 0 && !settledDrag {
				d.WaitSettled(ctx, time.Second)
				r.invalidate()
				settledDrag = true
				continue
			}
			if fresh == 0 {
				fruitless++
			} else {
				fruitless, endWaited = 0, false
			}
			if fruitless >= 2 && vertical {
				markTried(s, x1, y1, tried)
				if box := otherList(s, tried); box != nil {
					tried[listKey(box)] = true
					placeVertical(box)
					fruitless, endWaited = 0, false
					r.tracef("2 drags brought nothing new here: trying the other list, %s", nodeDesc(box))
				}
			}
			if fruitless >= 2 && !fling {
				why := "a paging list springs back, a list at its end, or touches that did not reach the app"
				if neverConfirmedRun > 0 {
					why = "whether the last one reached the app is unknown: no process reported its touches"
					if neverConfirmedRun > 1 {
						why = "whether either reached the app is unknown: no process reported their touches"
					}
				}
				r.tracef("2 drags in a row brought nothing new into view (%s): flinging from now on", why)
			}
			fling = fruitless >= 2
		} else if dragged.IsZero() {
			newTexts(s, x1, y1, seen)
		}
		if !ready {
			again, err := r.scrollReady(ctx, readyWatch, &readyAt, s, x1, y1, deadline)
			if err != nil {
				return err
			}
			if again {
				continue
			}
			ready = true
			readyWatch.Stop()
		}
		if fling && !partial {
			sx, sy, ex, ey, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, fx1, fy1, x2, y2)
			if !ok {
				noRoom(s)
				continue
			}
			flingFrom := listSignature(s, x1, y1, vertical)
			r.invalidate()
			sending(s)
			r.mark("swipe", [2]int{sx, sy}, [2]int{ex, ey})
			guard = nil
			res, err := d.Swipe(ctx, sx, sy, ex, ey, dur, driver.ActionOpts{NoChangeMs: 300})
			if err != nil {
				return err
			}
			confirmed(res)
			flung = true
			r.tracef("fling %d: (%d, %d) -> (%d, %d): %s", swipes, sx, sy, ex, ey, actionDesc(res))
			if res.Settle == driver.NoChange && n == nil {
				again, err := sendAgain(flingFrom)
				if err != nil {
					return err
				}
				if again {
					flung, unconfirmed, dragged = false, false, time.Time{}
					continue
				}
			} else {
				deliveryToken = 0
				if res.Settle != driver.NoChange {
					againRun = 0
				}
			}
			dragged, last, stillSince = time.Now(), nil, time.Time{}
			before = 0
			centerSent = false
			continue
		}
		if vertical && !placed {
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
			placed = true
			listed := listedIn(s, n)
			if box := sidewaysList(s, listed, w/2, h/2, sel.Text); box != nil {
				bringIns := 2
				if view != screen {
					bringIns = 4
				}
				if broughtIn > 0 && broughtIn < bringIns && box.Bounds == bringInFrom {
					r.tracef("bring-in drag %d moved nothing: dragging the list where it is", broughtIn)
					broughtIn = bringIns
				}
				if shown := box.Bounds.Intersect(view); shown.H()*10 < box.Bounds.H()*6 && broughtIn < bringIns {
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
						centerSent = false
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
		dx, dy, tx, ty := x1, y1, x2, y2
		var g *dragGuard
		if vertical && !centering && !partial {
			if lx, ly, ltx, lty, lg, ok := longDrag(s, view, x1, y1, y2, c.Direction, ownKey, !unguarded); ok {
				dx, dy, tx, ty, g = lx, ly, ltx, lty, lg
			}
		}
		if centering {
			centerTries++
			if vertical {
				stride := abs(y2 - y1)
				ty = y1 + clamp(view.CY()-n.Bounds.CY(), -stride, stride)
			} else {
				stride := abs(x2 - x1)
				tx = x1 + clamp(view.CX()-n.Bounds.CX(), -stride, stride)
			}
		} else if partial {
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
				ty = dy + clamp(ty-dy, -stride, stride)
			}
		}
		if vertical && shortBy > 0 {
			most := shortCap(s, view, x1, y1, ownKey, shortBy)
			if abs(ty-dy) > most {
				r.tracef("the last drag moved %d px less than its length: this one at most %d px, should the rest land with it", shortBy, most)
				ty = dy + clamp(ty-dy, -most, most)
			}
		}
		sx, sy, tx, ty, ok := clearOfKeyboardSwipe(s, d.Platform(), screen, dx, dy, tx, ty)
		if centering && d.Platform() == "android" && abs(tx-sx)+abs(ty-sy) < minDrag(screen) {
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
		centerSent = centering && !nudgePending
		if n != nil {
			centerFrom = n.Bounds
		}
		if g != nil {
			g.x, g.sy, g.ty, g.travel = sx, sy, ty, abs(ty-sy)
		}
		guard, shortBy = g, 0
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
