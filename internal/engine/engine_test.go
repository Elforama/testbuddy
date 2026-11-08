package engine

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// An element that appears later is found when the device reports the change, with no
// reads in between (no polling).
func TestFindWaitsOnEvents(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	shown := atomic.Bool{}
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{f.node("Welcome", "", 100)}
		}
		return []*ui.Node{f.node("Loading", "", 100)}
	}
	go func() {
		time.Sleep(600 * time.Millisecond)
		shown.Store(true)
		f.change(true)
	}()
	start := time.Now()
	if _, err := run(t, f, `- assertVisible: "Welcome"`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 500*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("found after %s; want right after the change at 600ms", d)
	}
	if n := f.reads.Load(); n > 3 {
		t.Errorf("%d screen reads while waiting: the wait polled", n)
	}
}

// assertNotVisible passes on a screen whose ticker never stops changing.
func TestAbsentOnRestlessScreen(t *testing.T) {
	f := &fakeDriver{}
	var tick atomic.Int64
	f.screen = func() []*ui.Node {
		return []*ui.Node{f.node("Live: "+string(rune('0'+tick.Load()%10)), "ticker", 100)}
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				tick.Add(1)
				f.change(false) // restless: changes keep coming, never "settled"
			}
		}
	}()
	start := time.Now()
	if _, err := run(t, f, `- assertNotVisible: "Spinner"`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("assertNotVisible took %s on a never-still screen", d)
	}
}

// A tap on a never-still screen happens at once, at the element's centre.
func TestTapWhileTicking(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		return []*ui.Node{f.node("Live", "ticker", 0), f.node("Tap me", "tap_me", 200)}
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
				f.change(false)
			}
		}
	}()
	start := time.Now()
	if _, err := run(t, f, `- tapOn: {id: tap_me}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("tap took %s", d)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 225} {
		t.Errorf("taps = %v, want one at (200, 225)", f.taps)
	}
}

// Optional lookups that fail are warnings; the flow continues.
func TestOptionalWarns(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { return []*ui.Node{f.node("Home", "", 0)} }
	res, err := run(t, f, "- tapOn: {text: Nope, optional: true}\n- assertVisible: Home")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 || res.Steps[0].Status != Warned || res.Steps[1].Status != Passed {
		t.Errorf("steps = %+v", res.Steps)
	}
}

// list is a scrolling list of n rows, 50 px each, moved up by the offset; a drag moves it by
// the drag's length (no fling), up to the list's end.
func (f *fakeDriver) list(n int) *atomic.Int64 {
	var off atomic.Int64
	f.screen = func() []*ui.Node {
		box := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		nodes := []*ui.Node{box}
		for i := 0; i < n; i++ {
			top := 100 + i*50 - int(off.Load())
			if top+50 <= 0 || top >= 800 {
				continue // off screen: a recycled row
			}
			row := f.node(fmt.Sprintf("Item %d", i), "", top)
			row.Depth = 1
			nodes = append(nodes, row)
		}
		return nodes
	}
	end := int64(max(0, 100+n*50-800))
	f.onSwipe = func(x1, y1, x2, y2 int) {
		o := min(off.Load()+int64(y1-y2), end)
		if o != off.Load() {
			off.Store(o)
			f.change(true)
		}
	}
	return &off
}

// scrollUntilVisible drags without flinging and drags again right after each read, with no
// settle between drags.
func TestScrollDragsWithoutWaiting(t *testing.T) {
	f := &fakeDriver{}
	f.list(60)
	start := time.Now()
	if _, err := run(t, f, `- scrollUntilVisible: {element: "Item 40"}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("scroll took %s", d)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Item 40 starts at 2100 px; each drag (center to 10%) moves 320 px.
	if len(f.swipes) != 5 {
		t.Errorf("%d drags, want 5", len(f.swipes))
	}
	for _, o := range f.swipes {
		if !o.Drag || !o.NoSettle {
			t.Errorf("swipe options %+v: want a drag with no settle", o)
		}
	}
}

// At the list's end a drag moves nothing: the next one waits for the screen to change
// rather than dragging over and over.
func TestScrollStopsDraggingAtListEnd(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.list(20)
	_, err := run(t, f, `- scrollUntilVisible: {element: "Item 99", timeout: 2500}`)
	if err == nil || !strings.Contains(err.Error(), "No visible element found") {
		t.Fatalf("err = %v, want not found", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.swipes) > 5 {
		t.Errorf("%d drags at the list's end in 2.5 s: it did not wait for a change", len(f.swipes))
	}
}

// A tap returns once the UI starts reacting. An assertion after it runs at once; a command
// that needs a still screen (typing) waits for the UI to settle first.
func TestTapDefersSettle(t *testing.T) {
	for _, tc := range []struct {
		next  string
		waits int64
	}{
		{"- assertVisible: Done", 1}, // findStable before the tap only
		{"- inputText: hello", 2},    // and the settle the tap left
		{"- tapOn: {text: Done}", 3}, // the next tap settles before it looks, then as always
	} {
		f := &fakeDriver{}
		f.screen = func() []*ui.Node { return []*ui.Node{f.node("Go", "go", 0), f.node("Done", "", 100)} }
		if _, err := run(t, f, "- tapOn: {id: go}\n"+tc.next); err != nil {
			t.Fatal(err)
		}
		if n := f.waits.Load(); n != tc.waits {
			t.Errorf("%s: %d settle waits, want %d", tc.next, n, tc.waits)
		}
		if !f.tapOpts[0].React {
			t.Errorf("tap options %+v: want React", f.tapOpts[0])
		}
	}
}

// A paging list springs back from a drag shorter than a page: after two drags that bring
// nothing new into view, the swipes fling (classic swipes), which turn the page.
func TestScrollFlingsWhenDragsDoNotMove(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var page atomic.Int64
	f.screen = func() []*ui.Node {
		box := &ui.Node{Class: "pager", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		row := f.node(fmt.Sprintf("Page %d", page.Load()), "", 300)
		row.Depth = 1
		return []*ui.Node{box, row}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		f.mu.Lock()
		o := f.swipes[len(f.swipes)-1]
		f.mu.Unlock()
		if !o.Drag { // only a fling turns the page
			page.Add(1)
			f.change(true)
		}
	}
	if _, err := run(t, f, `- scrollUntilVisible: {element: "Page 2", timeout: 10000}`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	flings := 0
	for _, o := range f.swipes {
		if !o.Drag {
			flings++
		}
	}
	if flings != 2 {
		t.Errorf("%d flings, want 2 (swipes: %d)", flings, len(f.swipes))
	}
}

// A row under a sticky footer is dragged just clear of it, then tapped where it shows.
func TestTapClearsFooter(t *testing.T) {
	f := &fakeDriver{}
	f.list(30)
	rows := f.screen
	f.screen = func() []*ui.Node {
		footer := f.node("Checkout", "footer", 700) // drawn after the list, over its bottom
		footer.Bounds = ui.Rect{R: 400, T: 700, B: 800}
		return append(rows(), footer)
	}
	// Item 12's row starts at 700 (100 + 12*50): its middle (725) is under the footer.
	if _, err := run(t, f, `- tapOn: "Item 12"`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.swipes) != 1 || !f.swipes[0].Drag {
		t.Fatalf("swipes = %+v, want one drag", f.swipes)
	}
	if tap := f.taps[0]; tap[1] >= 700 {
		t.Errorf("tapped at %v: under the footer", tap)
	}
}

// A button the keyboard half covers, on a screen that does not scroll, is tapped on its part
// above the keyboard.
func TestTapAvoidsKeyboard(t *testing.T) {
	f := &fakeDriver{keyboard: &ui.Rect{T: 600, R: 400, B: 800}}
	f.screen = func() []*ui.Node {
		b := f.node("Send", "send", 500)
		b.Bounds = ui.Rect{T: 500, R: 400, B: 700}
		return []*ui.Node{b}
	}
	if _, err := run(t, f, `- tapOn: {id: send}`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taps[0] != [2]int{200, 550} {
		t.Errorf("tapped at %v, want (200, 550) above the keyboard", f.taps[0])
	}
}

// A label drawn over the element takes no touches: no cover, the tap goes to the middle.
func TestTapThroughLabel(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		b := f.node("Buy", "buy", 100)
		l := &ui.Node{Text: "50% off", Bounds: ui.Rect{L: 150, T: 110, R: 250, B: 140}} // not clickable
		return []*ui.Node{b, l}
	}
	if _, err := run(t, f, `- tapOn: {id: buy}`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taps[0] != [2]int{200, 125} {
		t.Errorf("tapped at %v, want the middle (200, 125)", f.taps[0])
	}
}

// Android's "isn't responding" dialog is answered Wait the first time, Close app when it comes
// back; then the lookup goes on.
func TestAnswersAndroidErrorDialog(t *testing.T) {
	f := &fakeDriver{}
	var shows atomic.Int64
	shows.Store(2) // shows twice
	f.screen = func() []*ui.Node {
		if shows.Load() == 0 {
			return []*ui.Node{f.node("Home", "", 100)}
		}
		title := f.node("Launcher isn't responding", "android:id/alertTitle", 100)
		wait := f.node("Wait", "android:id/aerr_wait", 200)
		closeApp := f.node("Close app", "android:id/aerr_close", 300)
		return []*ui.Node{title, wait, closeApp}
	}
	f.onTap = func(x, y int) { shows.Add(-1); f.change(true) }
	if _, err := run(t, f, `- assertVisible: Home`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) != 2 || f.taps[0][1] != 225 || f.taps[1][1] != 325 {
		t.Errorf("taps = %v, want Wait (y 225) then Close app (y 325)", f.taps)
	}
}

// iOS's notifications alert is answered as launchApp's permissions say.
func TestAnswersNotificationAlert(t *testing.T) {
	for _, tc := range []struct {
		perms string
		want  int // the y of the button tapped, 0 for none
	}{
		{"", 525}, // default: all allowed
		{":\n    permissions: {notifications: deny}", 425}, // Don't Allow
		{":\n    permissions: {notifications: unset}", 0},  // left to the flow
	} {
		f := &fakeDriver{plat: "ios"}
		var shown atomic.Bool
		shown.Store(true)
		f.screen = func() []*ui.Node {
			nodes := []*ui.Node{f.node("Home", "", 100)}
			if shown.Load() {
				msg := &ui.Node{Desc: "“Bench” Would Like to Send You Notifications", Bounds: ui.Rect{R: 400, T: 300, B: 380}}
				no := f.node("", "", 400)
				no.Class, no.Desc = "Button", "Don’t Allow"
				yes := f.node("", "", 500)
				yes.Class, yes.Desc = "Button", "Allow"
				nodes = append(nodes, msg, no, yes)
			}
			return nodes
		}
		f.onTap = func(x, y int) { shown.Store(false); f.change(true) }
		if _, err := run(t, f, "- launchApp"+tc.perms+"\n- assertVisible: Home"); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		got := 0
		if len(f.taps) > 0 {
			got = f.taps[0][1]
		}
		f.mu.Unlock()
		if got != tc.want {
			t.Errorf("permissions %q: tapped y %d, want %d", tc.perms, got, tc.want)
		}
	}
}

// A disabled button is tapped once it turns on (on its change event), not before.
func TestTapWaitsForEnabled(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var on atomic.Bool
	f.screen = func() []*ui.Node {
		b := f.node("Save", "save", 100)
		b.Enabled = on.Load()
		return []*ui.Node{b}
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		on.Store(true)
		f.change(true)
	}()
	start := time.Now()
	if _, err := run(t, f, `- tapOn: {id: save}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("tapped after %s; want right after it turned on (300 ms)", d)
	}
	// Asked for as disabled: tapped at once.
	f2 := &fakeDriver{}
	f2.screen = func() []*ui.Node { b := f2.node("Save", "save", 100); b.Enabled = false; return []*ui.Node{b} }
	start = time.Now()
	if _, err := run(t, f2, `- tapOn: {id: save, enabled: false}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("tap on a selector asking for disabled took %s", d)
	}
}

// Checks for things that do not show (`runFlow` with `when: visible`) share one optional
// timeout counted from the last command that acted on the app: a runFlow whose commands only
// checked the screen did not act, so the next check does not wait the whole timeout again.
func TestOptionalChecksShareTheTimeout(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 100))}
	check := func(name string) string {
		return "- runFlow:\n    commands:\n      - runFlow:\n          when:\n            visible: " + name +
			"\n          commands:\n            - tapOn: " + name + "\n"
	}
	o := Options{LookupTimeout: time.Second, OptionalTimeout: 400 * time.Millisecond}
	start := time.Now()
	mustPass(t, runWith(t, f, o, check("Promo")+check("Survey")+check("Rate us")))
	if d := time.Since(start); d > 750*time.Millisecond {
		t.Errorf("three checks took %s, want about one optional timeout (400 ms)", d)
	}
	// A command that acts restarts the clock: the check after it waits again.
	start = time.Now()
	mustPass(t, runWith(t, f, o, check("Promo")+"- tapOn: Home\n"+check("Survey")))
	if d := time.Since(start); d < 700*time.Millisecond {
		t.Errorf("took %s: the check after a tap should wait its own timeout", d)
	}
}

// On the web, startRecording is not supported yet: the flow goes on, with a note, and
// stopRecording does nothing.
func TestWebRecordingSkipped(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 100)), plat: "web"}
	var mu sync.Mutex
	var logs []string
	o := quick
	o.ArtifactsDir = t.TempDir()
	o.Reporter = ReporterFunc(func(e Event) {
		if e.Log != "" {
			mu.Lock()
			logs = append(logs, e.Log)
			mu.Unlock()
		}
	})
	mustPass(t, runWith(t, f, o, "- startRecording: demo\n- assertVisible: Home\n- stopRecording\n"))
	if len(f.recs) != 0 {
		t.Errorf("recordings started: %v", f.recs)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || !strings.Contains(logs[0], "not supported on web") || !strings.Contains(logs[0], "demo.mp4") {
		t.Errorf("logs %q", logs)
	}
}
