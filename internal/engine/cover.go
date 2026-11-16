package engine

import (
	"bytes"
	"context"
	"image"
	_ "image/png"
	"os"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

// Covers. Something drawn over an element where a tap would land takes the tap instead: a
// bar fixed over a list (a tab bar, a toolbar, a sticky footer), a floating button, the
// keyboard. A plain tap at the element's center would hit it. tb looks for a cover there first;
// when there is one it drags the element clear in its list, as a person would, and failing
// that taps the largest part of the element that shows. A drag is only for bars: a cover that
// is a modal layer (a drawer, a sheet, a dialog) lies over the content as a whole, and a drag
// would scroll what is under it; the tap waits for the layer to go instead (issue #282). What
// covers what is package occlusion's rule, the one the matcher ranks matches by.

var debug = os.Getenv("TB_DEBUG") != ""

// maxCoverClears is how many drags may try to bring an element clear of a cover.
const maxCoverClears = 2

// modalCoverWait caps the wait for a modal layer over a tap's target to go (a drawer or sheet
// closing): then the step fails, saying what covers the target (or is skipped, optional).
const modalCoverWait = 2 * time.Second

// The geometry of covers, shared with the matcher (package occlusion).
var (
	coverAt        = occlusion.At
	visiblePart    = occlusion.VisiblePart
	uncoveredPoint = occlusion.UncoveredPoint
	stickyHeader   = occlusion.StickyHeader
	within         = occlusion.Within
	takesTouches   = occlusion.TakesTouches
	touchable      = occlusion.Touchable
	inIOSBar       = occlusion.InIOSBar
)

// clearCover drags n's list so n comes clear of cover: a bar along the top or bottom moves the
// content down or up, by just enough plus a margin, from the middle of the list's part that
// shows clear of the cover. It returns false when no drag can: n is in no list that scrolls up
// and down, or the cover is in that list too (it moves along), or the list shows too little.
func (r *Runner) clearCover(ctx context.Context, s *ui.Screen, n, cover *ui.Node) (bool, error) {
	c := cover.Bounds
	if c.W() < c.H() {
		return false, nil // a bar along a side
	}
	var list *ui.Node
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && p.Bounds.H() >= p.Bounds.W()/2 {
			list = p
			break
		}
	}
	if list == nil && s.Platform == "web" {
		// The page itself scrolls (the window, which the tree does not list as an element):
		// the web's drag scrolls what scrolls under the point, else the page (issue #300). It
		// only brings n clear of a cover that stays put while the page scrolls: a fixed or
		// sticky one. One in the page's flow (a stretched link over a card) moves along.
		if !occlusion.Positioned(n, cover) {
			return false, nil
		}
		list = &ui.Node{Class: "page", Scrollable: true, Bounds: ui.Rect{R: s.Width, B: s.Height}}
	}
	if list == nil || cover.IsDescendantOf(list) {
		return false, nil
	}
	lo, hi := max(list.Bounds.T, 0), min(list.Bounds.B, s.Height)
	bottom := c.CY() > s.Height/2
	if bottom {
		hi = min(hi, c.T)
	} else {
		lo = max(lo, c.B)
	}
	if hi-lo < s.Width/6 {
		return false, nil
	}
	margin := s.Width / 12
	shift := c.B - n.Bounds.T + margin // the cover is above: content down
	if bottom {
		shift = -(n.Bounds.B - c.T + margin)
	}
	x := clamp(list.Bounds.CX(), 0, s.Width-1)
	mid := (lo + hi) / 2
	half := (hi - lo) / 2
	r.invalidate()
	r.mark("swipe", [2]int{x, mid}, [2]int{x, mid + clamp(shift, -half, half)})
	if _, err := r.Drv.Swipe(ctx, x, mid, x, mid+clamp(shift, -half, half), 0, driver.ActionOpts{NoSettle: true, Drag: true}); err != nil {
		return false, err
	}
	r.Drv.WaitSettled(ctx, 2*time.Second)
	return true, nil
}

// clearAndPoint brings n (found by sel on s) clear of anything covering it and says where to
// tap it. Up to maxCoverClears drags of its list, each followed by finding it again; then its
// largest uncovered part; failing that its center.
func (r *Runner) clearAndPoint(ctx context.Context, sel *match.Selector, s *ui.Screen, n *ui.Node) (x, y int, err error) {
	x, y, cover, ok := uncoveredPoint(s, n)
	if cover != nil {
		r.tracef("covered at its middle by %s", nodeDesc(cover))
		if layer := occlusion.Modal(s, n, cover); layer != nil {
			// No drag (it would scroll the content under the layer, or close it), and no tap
			// (it would land on the layer, on whatever is there). The tap waited for it to go
			// (awaitModalGone).
			r.tracef("that is in a modal layer (%s), not a bar: no drag can bring it clear", nodeDesc(layer))
			return 0, 0, underModal(sel, s, n, cover)
		}
	}
	for i := 0; cover != nil && i < maxCoverClears; i++ {
		was := n.Bounds
		dragged, err := r.clearCover(ctx, s, n, cover)
		if err != nil || !dragged {
			if err == nil {
				r.tracef("no drag can bring it clear (not in a list that scrolls, or the cover moves with it)")
			}
			break
		}
		r.tracef("dragged its list to bring it clear of the cover")
		n2, s2, _, ferr := r.find(ctx, sel, time.Second)
		if ferr != nil {
			return 0, 0, ferr
		}
		n, s = n2, s2
		x, y, cover, ok = uncoveredPoint(s, n)
		if cover != nil && occlusion.Modal(s, n, cover) != nil {
			return 0, 0, underModal(sel, s, n, cover)
		}
		if n.Bounds == was {
			break // the list moves no further
		}
	}
	if cover != nil && !ok && s.Keyboard {
		// Nothing shows clear and the keyboard is up: it is likely what covers the element
		// (iOS can list the keyboard's views in the app's own window, as no keyboard window).
		// Hide it, as a person would, and find the element again.
		r.tracef("the keyboard is up: hiding it (it may be the cover) and looking again")
		if herr := r.Drv.HideKeyboard(ctx); herr != nil {
			r.tracef("hiding the keyboard failed: %v", herr)
		}
		r.invalidate()
		n2, s2, _, ferr := r.find(ctx, sel, time.Second)
		if ferr != nil {
			return 0, 0, ferr
		}
		n, s = n2, s2
		x, y, cover, ok = uncoveredPoint(s, n)
	}
	if cover != nil && !ok {
		v := visiblePart(s, n)
		r.tracef("no part of it shows clear of %s: tapping its middle anyway", nodeDesc(cover))
		return v.CX(), v.CY(), nil
	}
	if cover != nil {
		r.tracef("tapping its largest uncovered part, at (%d, %d)", x, y)
	}
	return x, y, nil
}

// awaitModalGone: n (found by sel on s) is under a modal layer (occlusion.Covered), and no
// other match shows clear (the matcher ranks those first). A tap there lands on the layer, and
// no drag brings n clear of it: wait, on change events, for the layer to go (a drawer or sheet
// closing), up to timeout, finding n again at each change. Still under it then, the step
// fails (underModal): a tap would land on the layer, on whatever is there.
func (r *Runner) awaitModalGone(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, timeout time.Duration) (*ui.Node, *ui.Screen, error) {
	c, _ := occlusion.Covered(s, n)
	start := time.Now()
	r.tracef("it is under a modal layer (%s, at %s): waiting up to %s for it to go",
		nodeDesc(occlusion.Modal(s, n, c)), nodeDesc(c), timeout.Round(time.Millisecond))
	events, stop := r.Drv.Events()
	defer stop()
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		r.waitAny(ctx, events, s.Seq, deadline)
		if ctx.Err() != nil {
			return n, s, ctx.Err()
		}
		r.invalidate()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			continue
		}
		n, s = n2, s2
		if _, modal := occlusion.Covered(s, n); !modal {
			r.tracef("it shows clear after %s", since(start))
			return n, s, nil
		}
	}
	c, _ = occlusion.Covered(s, n)
	r.tracef("still under the layer after %s: not tapping it (the tap would land on the layer)", since(start))
	return n, s, underModal(sel, s, n, c)
}

// underModal is the failure of a step whose target stays under a modal layer.
func underModal(sel *match.Selector, s *ui.Screen, n, cover *ui.Node) error {
	what := nodeDesc(cover)
	if layer := occlusion.Modal(s, n, cover); layer != nil && layer != cover {
		what += " in " + nodeDesc(layer)
	}
	return failf("%s is under %s", sel.Describe(), what)
}

// ---- covers that leave the tree but stay drawn ----
//
// A screen, sheet, menu or list over another can leave the accessibility tree at once when it
// closes and yet stay drawn, perfectly still, and take touches for a while: a React Native
// screen closing while its JavaScript thread is busy (bench/flows/scenarios/transitions.yaml).
// What was under it is listed at once, and nothing in the tree or the event stream says it is
// still there: a tap on an element under it would land on it. Only the pixels show it. So a
// tap on something drawn over other listed elements takes a screenshot first; when a later
// read no longer lists that cover, the next tap on an element that was under it compares the
// screen where it taps with that screenshot. Unchanged pixels there are the cover's: the tap
// waits until they change. A cover really gone shows what is under it at once: no wait.

// heldCoverWait caps that wait: the bench's list stays 800 ms, a busy JavaScript thread a
// second or two. A cover whose pixels never change (or an element drawn exactly as the cover
// was) costs at most this, then the tap goes anyway, as before.
const heldCoverWait = 3 * time.Second

// heldPoll: how often the pixels are looked at again while the device reports no change. A
// cover's removal is not always reported (it was out of the tree already), and a screenshot
// taken just after the change can still show the frame before.
const heldPoll = 150 * time.Millisecond

// heldShotScale: the screenshots compared are a quarter of the screen's size (fast to take and
// compare; an element still spans many pixels).
const heldShotScale = 0.25

// heldCover is a cover a tap was made in, and the screen as drawn just before that tap.
type heldCover struct {
	window int
	pkg    string
	cover  nodeKey   // the container drawn over the others
	parts  []nodeKey // what takes touches in it (to tell it from another node in its place)
	under  []nodeKey // the listed elements it was over
	shot   image.Image
}

// nodeKey is enough of a node to find it again in a later read.
type nodeKey struct {
	class, id, text string
	bounds          ui.Rect
}

func keyOf(n *ui.Node) nodeKey { return nodeKey{n.Class, n.ID, n.Label(), n.Bounds} }

func near(a, b ui.Rect, d int) bool {
	return abs(a.L-b.L) <= d && abs(a.T-b.T) <= d && abs(a.R-b.R) <= d && abs(a.B-b.B) <= d
}

// coverOver is the container around n (n itself, or one of its containers in its window)
// drawn over other listed elements that take touches, and those elements; nil when there is
// none, as for most taps. The highest such container is the whole cover (a sheet, not its
// row). An element counts when it lies wholly inside the container, outside it in the tree,
// the container takes touches at its middle (a row of it is there: a see-through layer that
// holds only a tab bar takes no tap meant for the content under it), and it is drawn under
// the container: Android reports the drawing order; where it does not tell (iOS), two such
// elements are needed, so that one floating button over a list does not make the list a
// cover. Not on the web (the browser's hit test sees covers), nor for iOS's bars.
func coverOver(s *ui.Screen, n *ui.Node) (*ui.Node, []*ui.Node) {
	if s.Platform == "web" || n.Window < 0 || n.Window >= len(s.Windows) || s.Windows[n.Window].Type != "app" {
		return nil, nil
	}
	win := s.Windows[n.Window]
	var cover *ui.Node
	for a := n; a != nil; a = a.Parent {
		if a.Bounds.Empty() {
			continue
		}
		var sure, maybe []*ui.Node
		for _, c := range win.Nodes {
			if c.Bounds.Empty() || c.IsDescendantOf(a) || a.IsDescendantOf(c) || !within(c.Bounds, a.Bounds) ||
				!touchable(s.Platform, c) || (s.Platform == "ios" && inIOSBar(c)) ||
				!takesTouches(s.Platform, a, c.Bounds.CX(), c.Bounds.CY()) {
				continue
			}
			switch under, known := drawOrder(c, a); {
			case known && under:
				sure = append(sure, c)
			case !known:
				maybe = append(maybe, c)
			}
		}
		if len(sure) > 0 || len(maybe) > 1 {
			cover = a
		}
	}
	if cover == nil {
		return nil, nil
	}
	// Everything listed under it, not only what takes touches: any of it may be tapped next.
	var covered []*ui.Node
	for _, c := range win.Nodes {
		if !c.Bounds.Empty() && !c.IsDescendantOf(cover) && !cover.IsDescendantOf(c) && within(c.Bounds, cover.Bounds) {
			if under, known := drawOrder(c, cover); under || !known {
				covered = append(covered, c)
			}
		}
	}
	return cover, covered
}

// noteCover: the tap about to be made on n (read on s) is in a cover (coverOver). Its
// screenshot, taken now, is kept for the next tap (Runner.held). nil: no cover, or no
// screenshot.
func (r *Runner) noteCover(ctx context.Context, s *ui.Screen, n *ui.Node) *heldCover {
	cover, under := coverOver(s, n)
	if cover == nil {
		return nil
	}
	t := time.Now()
	shot, err := r.heldShot(ctx)
	if err != nil {
		r.tracef("a screenshot before tapping in %s failed: %v", nodeDesc(cover), err)
		return nil
	}
	w := s.Windows[cover.Window]
	h := &heldCover{window: w.ID, pkg: w.Package, cover: keyOf(cover), shot: shot}
	cover.Walk(func(d *ui.Node) bool {
		if d != cover && touchable(s.Platform, d) && len(h.parts) < 8 {
			h.parts = append(h.parts, keyOf(d))
		}
		return true
	})
	for _, u := range under {
		h.under = append(h.under, keyOf(u))
	}
	r.tracef("it is in %s, drawn over %d listed elements: took a screenshot (%s), to see if it stays drawn once it leaves the tree",
		nodeDesc(cover), len(under), since(t))
	return h
}

func (r *Runner) heldShot(ctx context.Context) (image.Image, error) {
	b, err := r.Drv.Screenshot(ctx, heldShotScale, "png")
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

// listed: the cover is still in the tree s lists (in its place, with what it held).
func (h *heldCover) listed(s *ui.Screen) bool {
	for _, m := range s.Nodes {
		if m.Class != h.cover.class || m.ID != h.cover.id || !near(m.Bounds, h.cover.bounds, 2) {
			continue
		}
		if len(h.parts) == 0 {
			return true
		}
		found := false
		m.Walk(func(d *ui.Node) bool {
			for _, p := range h.parts {
				if d != m && d.Class == p.class && d.ID == p.id && d.Label() == p.text {
					found = true
				}
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// wasUnder: n is one of the elements the cover was over.
func (h *heldCover) wasUnder(n *ui.Node) bool {
	for _, u := range h.under {
		if u.class == n.Class && u.id == n.ID && near(u.bounds, n.Bounds, 4) && (n.ID != "" || u.text == n.Label()) {
			return true
		}
	}
	return false
}

// A faded native view can leave the visible tree before touch routing changes.
// For an identified prior cover, let a capable agent wait for actual removal.
func (r *Runner) awaitMountedCover(ctx context.Context, h *heldCover, s *ui.Screen, n *ui.Node, x, y int, timeout time.Duration) (bool, error) {
	d, ok := r.Drv.(driver.MountedCoverWaiter)
	if !ok || h == nil || h.cover.id == "" || !h.cover.bounds.Contains(x, y) || n.Window < 0 || n.Window >= len(s.Windows) {
		return false, nil
	}
	w := s.Windows[n.Window]
	if w.ID != h.window || w.Package != h.pkg {
		return false, nil
	}
	for _, c := range w.Nodes {
		if c.ID == h.cover.id && c.Class == h.cover.class && (n == c || n.IsDescendantOf(c)) {
			return false, nil // another action inside the same cover is intentional
		}
	}
	start := time.Now()
	observed, gone, err := d.AwaitCoverGone(ctx, h.window, h.pkg, h.cover.id, h.cover.class, x, y, timeout)
	if err != nil {
		return false, err
	}
	if observed && !gone {
		return false, failf("Closing overlay %s is still mounted over the target", h.cover.id)
	}
	if observed {
		r.tracef("waited %s on device callbacks for mounted overlay %s to leave the target", since(start), h.cover.id)
	}
	return observed, nil
}

// awaitHeldCover: the last tap was in cover h. If the read s no longer lists it, n was under
// it, and the screen where n is tapped, at (x, y), is as it was before that tap, the cover is
// still drawn there: wait, on change events and short polls, until those pixels change (up
// to timeout; then n is tapped anyway). It says whether it waited (the caller finds n again).
func (r *Runner) awaitHeldCover(ctx context.Context, h *heldCover, s *ui.Screen, n *ui.Node, x, y int, timeout time.Duration) bool {
	if h == nil || h.listed(s) || !h.wasUnder(n) {
		return false
	}
	region := visiblePart(s, n).Intersect(h.cover.bounds)
	for _, w := range s.Windows {
		if w.Type == "keyboard" && w.Bounds.T > region.T && w.Bounds.T < region.B {
			region.B = w.Bounds.T // what is under the keyboard does not show
		}
	}
	if region.Empty() || !region.Contains(x, y) {
		return false
	}
	start := time.Now()
	events, stop := r.Drv.Events()
	defer stop()
	shot, err := r.heldShot(ctx)
	if err != nil || !sameRegion(h.shot, shot, region, s.Width) {
		return false
	}
	r.tracef("%s left the tree but is still drawn where it was over this element (the pixels there are as before the last tap): waiting up to %s for it to go",
		nodeDesc(&ui.Node{Class: h.cover.class, ID: h.cover.id, Bounds: h.cover.bounds, Enabled: true}), timeout.Round(time.Millisecond))
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		r.waitAny(ctx, events, r.Drv.Seq(), minTime(deadline, time.Now().Add(heldPoll)))
		if ctx.Err() != nil {
			return true
		}
		if shot, err = r.heldShot(ctx); err != nil || !sameRegion(h.shot, shot, region, s.Width) {
			r.tracef("it went after %s", since(start))
			return true
		}
	}
	r.tracef("still drawn after %s: tapping anyway", since(start))
	return true
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// sameRegion: screenshots a and b (of a screen width units wide) show the same pixels in
// region r: no more than 1 in 200 of them differ by more than a tenth of a channel's range
// (scaling and compression noise stays below; a removed cover changes far more).
func sameRegion(a, b image.Image, r ui.Rect, width int) bool {
	if a == nil || b == nil || a.Bounds() != b.Bounds() || width <= 0 {
		return false
	}
	k := float64(a.Bounds().Dx()) / float64(width)
	px := image.Rect(int(float64(r.L)*k)+1, int(float64(r.T)*k)+1, int(float64(r.R)*k)-1, int(float64(r.B)*k)-1).
		Add(a.Bounds().Min).Intersect(a.Bounds())
	if px.Empty() {
		return false
	}
	const tol = 0x1999 // a tenth of 0xffff
	differ := 0
	for y := px.Min.Y; y < px.Max.Y; y++ {
		for x := px.Min.X; x < px.Max.X; x++ {
			r1, g1, b1, _ := a.At(x, y).RGBA()
			r2, g2, b2, _ := b.At(x, y).RGBA()
			if absDiff(r1, r2) > tol || absDiff(g1, g2) > tol || absDiff(b1, b2) > tol {
				differ++
			}
		}
	}
	return differ*200 <= px.Dx()*px.Dy()
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
