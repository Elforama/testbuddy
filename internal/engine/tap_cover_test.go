package engine

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// hitFake is a web-like driver whose hit test reports a cover while veiled is set.
type hitFake struct {
	*fakeDriver
	veiled atomic.Bool
	checks atomic.Int64
}

func (h *hitFake) CoveredAt(ctx context.Context, index, x, y int) (string, error) {
	h.checks.Add(1)
	if h.veiled.Load() {
		return "#veil", nil
	}
	return "", nil
}

// A web tap whose point a veil covers waits for the veil to go (the browser's hit test says
// so), then taps: the veil would swallow the tap.
func TestTapWaitsForHitClear(t *testing.T) {
	t.Parallel()
	h := &hitFake{fakeDriver: &fakeDriver{screen: static(btn("Under", "under", 300)), plat: "web"}}
	h.veiled.Store(true)
	var tappedVeiled atomic.Bool
	h.onTap = func(x, y int) { tappedVeiled.Store(h.veiled.Load()) }
	go func() {
		time.Sleep(300 * time.Millisecond)
		h.veiled.Store(false)
		h.change(true)
	}()
	start := time.Now()
	mustPass(t, runWith(t, h, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if tappedVeiled.Load() || len(h.taps) != 1 {
		t.Errorf("taps %v, under the veil %v", h.taps, tappedVeiled.Load())
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Errorf("took %s, want the veil's 300 ms", d)
	}
	// Not covered: one check, no wait.
	h2 := &hitFake{fakeDriver: &fakeDriver{screen: static(btn("Go", "", 300)), plat: "web"}}
	mustPass(t, runWith(t, h2, quick, "- tapOn: Go"))
	if h2.checks.Load() != 1 {
		t.Errorf("%d hit checks for a clear tap", h2.checks.Load())
	}
}

// A web tap target pushed out of view (a banner grew above it) while something else moved into
// its place is not tapped "anyway": that would tap the other element. The step fails on the
// target instead.
func TestTapDoesNotHitWhatTookTheTargetsPlace(t *testing.T) {
	t.Parallel()
	h := &hitFake{fakeDriver: &fakeDriver{plat: "web"}}
	var reads atomic.Int64
	h.screen = func() []*ui.Node {
		if reads.Add(1) > 1 { // moved right after it was found
			h.veiled.Store(true)                            // the hit test at Stop's old place reaches Target
			return []*ui.Node{btn("Target", "target", 300)} // Stop is below the view now
		}
		return []*ui.Node{btn("Stop", "stop", 300)}
	}
	res := runWith(t, h, Options{LookupTimeout: 600 * time.Millisecond, OptionalTimeout: 300 * time.Millisecond}, "- tapOn: {id: stop}")
	for _, tap := range h.taps {
		t.Errorf("tapped %v: Target took Stop's place", tap)
	}
	if res.Err == nil {
		t.Error("passed without tapping Stop")
	}
}

// iOS: a tap on a button that got no reaction at all was taken by something tb cannot see (a
// veil hidden from accessibility). When the screen changes (the veil goes), the button is
// tapped again, and that tap reaches it.
func TestIOSSwallowedTapIsMadeAgain(t *testing.T) {
	t.Parallel()
	under := btn("Under", "under", 300)
	under.Class = "Button"
	f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
	var veiled atomic.Bool
	veiled.Store(true)
	var reached atomic.Int64
	f.onTap = func(x, y int) {
		if !veiled.Load() {
			reached.Add(1)
		}
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		veiled.Store(false)
		f.mu.Lock()
		f.tapSettle = driver.Changed
		f.mu.Unlock()
		f.change(true) // the veil went
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if reached.Load() != 1 {
		t.Errorf("taps %v, %d reached the button: want exactly one", f.taps, reached.Load())
	}
	// A button that does not react while nothing else changes is tapped once.
	g := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
	mustPass(t, runWith(t, g, quick, "- tapOn: Under"))
	if len(g.taps) != 1 {
		t.Errorf("no change anywhere: %d taps, want 1", len(g.taps))
	}
}

// iOS, on a screen that never stops moving: the tap lands (the counter goes up) but its
// reaction is filtered out with the screen's endless motion, so it reports no change. What the
// screen lists changed, so it is not tapped again (that would tap twice).
func TestIOSNoReactionButScreenChangedIsNotRetried(t *testing.T) {
	t.Parallel()
	var count atomic.Int64
	tap := btn("Tap me", "tap_me", 300)
	tap.Class = "Button"
	f := &fakeDriver{plat: "ios", tapSettle: driver.NoChange}
	f.screen = func() []*ui.Node {
		return []*ui.Node{tap, btn(fmt.Sprintf("Tapped %d", count.Load()), "tap_count", 100)}
	}
	f.onTap = func(x, y int) { count.Add(1); f.change(false) }
	mustPass(t, runWith(t, f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}, "- tapOn: {id: tap_me}"))
	if count.Load() != 1 {
		t.Errorf("tapped %d times, want 1", count.Load())
	}
}

// iOS: a veil's spinner is listed with the whole veil's frame. A tap on a button under it
// waits for it to go; a small spinner beside a button does not hold the tap.
func TestIOSVeilSpinnerHoldsTheTap(t *testing.T) {
	t.Parallel()
	under := btn("Under", "under", 600)
	under.Class = "Button"
	var veiled atomic.Bool
	veiled.Store(true)
	f := &fakeDriver{plat: "ios"}
	f.screen = func() []*ui.Node {
		if veiled.Load() {
			spin := &ui.Node{Class: "ActivityIndicator", Text: "1", Bounds: ui.Rect{L: 0, T: 62, R: 400, B: 790}}
			return []*ui.Node{spin, under}
		}
		return []*ui.Node{under}
	}
	var underVeil atomic.Bool
	f.onTap = func(x, y int) { underVeil.Store(veiled.Load()) }
	go func() {
		time.Sleep(400 * time.Millisecond)
		veiled.Store(false)
		f.change(true)
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if underVeil.Load() || len(f.taps) != 1 {
		t.Errorf("taps %v, under the veil %v", f.taps, underVeil.Load())
	}
	// A spinner of its own size beside the button.
	small := &ui.Node{Class: "ActivityIndicator", Bounds: ui.Rect{L: 0, T: 540, R: 40, B: 580}}
	g := &fakeDriver{plat: "ios", screen: static(small, under)}
	start := time.Now()
	mustPass(t, runWith(t, g, quick, "- tapOn: Under"))
	if time.Since(start) > 300*time.Millisecond {
		t.Errorf("a small spinner held the tap for %s", time.Since(start))
	}
}

// A floating bar over a content layer that holds a spinner far away (a feed loading more):
// the layer is drawn under the bar, so it is no cover. Android lists the layer after the bar
// (by place, not as drawn); the drawing order says which is on top. Issue #11.
func TestLoadingCoverDrawnUnderTarget(t *testing.T) {
	const pb = "android.widget.ProgressBar"
	order := func(n *ui.Node, o int) *ui.Node { n.DrawOrder, n.DrawOrderKnown = o, true; return n }
	build := func(barOrder, layerOrder int, known bool) (*ui.Screen, *ui.Node, *ui.Node) {
		root := at("Frame", 0, 0, 0, 400, 800)
		bar := at("LinearLayout", 1, 100, 680, 300, 740)
		action := clickable(2, 120, 690, 280, 730)
		layer := at("FrameLayout", 1, 0, 0, 400, 800)
		rows := at("LinearLayout", 2, 0, 0, 400, 800)
		spin := at(pb, 3, 180, 100, 220, 140) // near the top, far from the bar
		if known {
			order(bar, barOrder)
			order(layer, layerOrder)
		}
		return linked("android", root, bar, action, layer, rows, spin), action, layer
	}
	if s, action, _ := build(1, 0, true); loadingCover(s, action) != nil {
		t.Error("a layer drawn under the bar was taken for a loading overlay over it")
	}
	if s, action, layer := build(0, 1, true); loadingCover(s, action) != layer {
		t.Error("a layer drawn over the bar (a real veil) is a cover")
	}
	if s, action, layer := build(0, 0, false); loadingCover(s, action) != layer {
		t.Error("drawing order unknown: as before, the layer is a cover")
	}
	if s, action, layer := build(1, 1, true); loadingCover(s, action) != layer {
		t.Error("drawn at once (equal orders): as before, the layer is a cover")
	}
}

// The tap itself: no wait for a layer drawn under the floating bar (it took the whole lookup
// time before, Issue #11).
func TestTapOnFloatingBarOverSpinningLayerDoesNotWait(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		root := at("Frame", 0, 0, 0, 400, 800)
		bar := at("LinearLayout", 1, 100, 680, 300, 740)
		bar.DrawOrder, bar.DrawOrderKnown = 1, true
		action := clickable(2, 120, 690, 280, 730)
		action.Text = "Bar action"
		layer := at("FrameLayout", 1, 0, 0, 400, 800)
		layer.DrawOrder, layer.DrawOrderKnown = 0, true
		return []*ui.Node{root, bar, action, layer, at("LinearLayout", 2, 0, 0, 400, 800),
			at("android.widget.ProgressBar", 3, 180, 100, 220, 140)}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, `- tapOn: "Bar action"`))
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: waited for a layer drawn under the bar", d)
	}
	if len(f.taps) != 1 || f.taps[0][1] < 690 || f.taps[0][1] > 730 {
		t.Errorf("taps %v, want one on the bar's button", f.taps)
	}
}

// closingList is an account list over a form that, once a row is picked, leaves the tree at
// once but stays drawn, still, and taking touches for hold (bench/flows/scenarios/transitions.yaml;
// a React Native screen closing while its JavaScript thread is busy). A touch on it in that
// time picks the row under it and starts the hold again. Its last row, "Credit card", lies
// over the form's amount field.
type closingList struct {
	f        *fakeDriver
	hold     time.Duration
	open     bool      // listed and drawn
	heldTill time.Time // drawn and taking touches, though out of the tree, until then
	account  string
	focused  bool      // the amount field took a tap
	picked   time.Time // the last pick
	tapped   time.Time // the amount field's tap
}

var (
	clList    = ui.Rect{T: 100, R: 400, B: 800}
	clRows    = []ui.Rect{{T: 100, R: 400, B: 164}, {T: 164, R: 400, B: 228}, {T: 228, R: 400, B: 800}}
	clNames   = []string{"Checking", "Savings", "Credit card"}
	clPick    = ui.Rect{L: 20, T: 140, R: 200, B: 180}
	clAmount  = ui.Rect{L: 20, T: 300, R: 380, B: 340}
	clAccount = ui.Rect{L: 20, T: 110, R: 380, B: 130}
)

// drawn: the list shows (open, or closed but not yet removed). Called with f.mu held or not.
func (c *closingList) drawn() bool { return c.open || time.Now().Before(c.heldTill) }

func (c *closingList) nodes() []*ui.Node {
	ios := c.f.Platform() == "ios"
	kind := func(android, iosType string) string {
		if ios {
			return iosType
		}
		return android
	}
	node := func(class, id, text string, depth int, b ui.Rect, touch bool) *ui.Node {
		return &ui.Node{Class: class, ID: id, Text: text, Depth: depth, Bounds: b, Enabled: true, Clickable: touch, Editable: class == "EditText"}
	}
	d := 0 // the form's and the list's depth: on Android both sit in a frame
	var out []*ui.Node
	if !ios {
		out = append(out, node("FrameLayout", "", "", 0, clList, false))
		d = 1
	}
	list := node(kind("LinearLayout", "Other"), "tr_list", "", d, clList, false)
	list.DrawOrder, list.DrawOrderKnown = 2, !ios
	switch {
	case c.open:
		out = append(out, list)
		for i, r := range clRows {
			out = append(out, node(kind("TextView", "Button"), "tr_account_"+strings.ToLower(strings.Fields(clNames[i])[0]), clNames[i], d+1, r, true))
		}
	case ios && c.drawn():
		list.ID = "" // iOS keeps the closing view, with no id and nothing in it
		out = append(out, list)
	}
	fd := d
	if !ios {
		form := node("LinearLayout", "", "", d, clList, false)
		form.DrawOrder, form.DrawOrderKnown = 1, true
		out = append(out, form)
		fd = d + 1
	}
	out = append(out,
		node(kind("TextView", "StaticText"), "tr_account", "To: "+c.account, fd, clAccount, false),
		node(kind("Button", "Button"), "tr_pick", "Choose account", fd, clPick, true),
		node(kind("EditText", "TextField"), "tr_amount", "Amount", fd, clAmount, true))
	return out
}

func (c *closingList) image() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 400, 800))
	fill := func(r ui.Rect, col color.RGBA) {
		for y := r.T; y < r.B; y++ {
			for x := r.L; x < r.R; x++ {
				img.SetRGBA(x, y, col)
			}
		}
	}
	fill(ui.Rect{R: 400, B: 800}, color.RGBA{255, 255, 255, 255})
	f := c.f
	f.mu.Lock()
	drawn := c.drawn()
	f.mu.Unlock()
	if drawn {
		fill(clList, color.RGBA{235, 235, 235, 255})
		for _, r := range clRows {
			fill(ui.Rect{L: 20, T: r.T + 20, R: 140, B: r.T + 40}, color.RGBA{30, 30, 30, 255}) // its label
		}
		return img
	}
	fill(clAccount, color.RGBA{30, 30, 30, 255})
	fill(clPick, color.RGBA{40, 80, 200, 255})
	fill(clAmount, color.RGBA{20, 20, 20, 255}) // the border
	fill(ui.Rect{L: clAmount.L + 2, T: clAmount.T + 2, R: clAmount.R - 2, B: clAmount.B - 2}, color.RGBA{250, 250, 255, 255})
	fill(ui.Rect{L: 30, T: 312, R: 100, B: 328}, color.RGBA{150, 150, 150, 255}) // the hint
	return img
}

// tap is what a touch at (x, y) does. Called without f.mu held.
func (c *closingList) tap(x, y int) {
	f := c.f
	f.mu.Lock()
	if c.drawn() && clList.Contains(x, y) {
		for i, r := range clRows {
			if r.Contains(x, y) {
				c.account, c.open = clNames[i], false
				c.heldTill, c.picked = time.Now().Add(c.hold), time.Now()
			}
		}
		hold := c.hold
		f.mu.Unlock()
		f.change(true)
		if hold > 0 && hold < time.Minute {
			time.AfterFunc(hold, func() { f.change(true) }) // removed: the screen redraws
		}
		return
	}
	switch {
	case clPick.Contains(x, y):
		c.open = true
	case clAmount.Contains(x, y):
		c.focused, c.tapped = true, time.Now()
	}
	f.mu.Unlock()
	f.change(true)
}

func newClosingList(plat string, hold time.Duration) *closingList {
	c := &closingList{f: &fakeDriver{plat: plat}, hold: hold, account: "none"}
	c.f.screen = func() []*ui.Node { return c.nodes() } // f.mu is held here
	c.f.draw = c.image
	c.f.onTap = c.tap
	return c
}

// A tap on an element a closing screen still covers waits until the screen's pixels go
// (bench/flows/scenarios/transitions.yaml; a React Native screen closing while its
// JavaScript thread is busy): picking an account takes the list out of the tree at once, but
// it stays drawn, still, and takes touches for a while. The amount field under it is listed
// at once; nothing in the tree or the event stream says the list is still there, but the
// pixels where the field is are still the list's, as they were before the pick. The tap waits
// until they change. A list really gone at once costs no wait; one that never goes is waited
// for up to the cap, then tapped anyway, and the trace says so.
func TestTapWaitsForClosingScreenStillDrawn(t *testing.T) {
	t.Parallel()
	flow := "- tapOn: {id: tr_pick}\n- tapOn: {id: tr_account_savings}\n- tapOn: {id: tr_amount}\n"
	for _, plat := range []string{"android", "ios"} {
		t.Run(plat+", held", func(t *testing.T) {
			t.Parallel()
			const hold = 250 * time.Millisecond
			c := newClosingList(plat, hold)
			res, tr := traced(t, c.f, flow)
			mustPass(t, res)
			c.f.mu.Lock()
			defer c.f.mu.Unlock()
			if c.account != "Savings" || !c.focused {
				t.Fatalf("account %q, field tapped %v: want Savings kept, the field tapped once the list went\n%s", c.account, c.focused, tr)
			}
			if gap := c.tapped.Sub(c.picked); gap < hold {
				t.Errorf("the field was tapped %s after the pick, while the list still covered it (%s)", gap, hold)
			}
			wantLines(t, tr, `still drawn where it was`, `it went after \d+ ms`)
		})
		t.Run(plat+", gone at once", func(t *testing.T) {
			t.Parallel()
			c := newClosingList(plat, 0)
			res, tr := traced(t, c.f, flow)
			mustPass(t, res)
			shots := c.f.called("Screenshot")
			c.f.mu.Lock()
			defer c.f.mu.Unlock()
			if c.account != "Savings" || !c.focused {
				t.Fatalf("account %q, field tapped %v\n%s", c.account, c.focused, tr)
			}
			// Well under a hold (800 ms in the bench): no wait. Not tighter: a busy CI runner with
			// -race took 155 ms for the pick, a read and the screenshots.
			if gap := c.tapped.Sub(c.picked); gap > 500*time.Millisecond {
				t.Errorf("waited %s to tap the field, with the list gone at once", gap)
			}
			if strings.Contains(tr, "still drawn") {
				t.Errorf("waited for a list that went at once:\n%s", tr)
			}
			// One screenshot before the pick, one to see the list gone; none for the others.
			if len(shots) != 2 {
				t.Errorf("screenshots %v, want 2", shots)
			}
		})
		t.Run(plat+", never goes", func(t *testing.T) {
			t.Parallel()
			c := newClosingList(plat, time.Hour)
			res, tr := traced(t, c.f, flow) // quick: the tap's lookup timeout (400 ms) caps it
			mustPass(t, res)
			c.f.mu.Lock()
			defer c.f.mu.Unlock()
			if gap := time.Since(c.picked); gap > 2*time.Second {
				t.Errorf("waited %s, over the cap", gap)
			}
			wantLines(t, tr, `still drawn where it was`, `still drawn after \d+ ms: tapping anyway`)
		})
	}
}

// A tap in a see-through layer over the content takes no screenshot (bench/flows/scenarios/
// navigation.yaml on iOS: a full-screen container holding only the tab buttons, listed apart
// from the tab's content, whose order iOS does not tell): the layer takes no touches over the
// content's buttons, so it covers nothing a tap could miss. A list whose rows lie over two of
// a form's buttons does (bench/flows/scenarios/transitions.yaml).
func TestCoverOverNeedsTouchesOverWhatItCovers(t *testing.T) {
	t.Parallel()
	tab := &ui.Node{Class: "Button", ID: "navtab_feed", Depth: 1, Bounds: ui.Rect{L: 100, T: 740, R: 200, B: 800}, Enabled: true}
	layer := &ui.Node{Class: "Other", Bounds: ui.Rect{R: 400, B: 800}}
	content := []*ui.Node{
		{Class: "Button", ID: "open_post", Bounds: ui.Rect{L: 100, T: 300, R: 300, B: 340}, Enabled: true},
		{Class: "Button", ID: "share", Bounds: ui.Rect{L: 100, T: 400, R: 300, B: 440}, Enabled: true},
	}
	s := linked("ios", append([]*ui.Node{layer, tab}, content...)...)
	if c, _ := coverOver(s, tab); c != nil {
		t.Errorf("a tab in a see-through layer: cover %s, want none", nodeDesc(c))
	}
	rows := &ui.Node{Class: "Button", ID: "row", Depth: 1, Bounds: ui.Rect{T: 280, R: 400, B: 800}, Enabled: true}
	list := &ui.Node{Class: "Other", ID: "list", Bounds: ui.Rect{T: 280, R: 400, B: 800}}
	s = linked("ios", append([]*ui.Node{list, rows}, content...)...)
	if c, under := coverOver(s, rows); c == nil || c.ID != "list" || len(under) != 2 {
		t.Errorf("a list over two buttons: cover %s over %d, want the list over 2", nodeDesc(c), len(under))
	}
}
