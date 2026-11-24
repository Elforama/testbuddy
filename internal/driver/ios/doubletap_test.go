package ios

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestTapsOnOneSpotAreNotADoubleTap(t *testing.T) {
	const uikitDoubleTap = 350 * time.Millisecond
	r := newFakeRunner(t)
	var mu sync.Mutex
	var arrived []time.Time
	stamp := func(map[string]any) map[string]any {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		return map[string]any{"result": "changed", "seq": 1}
	}
	r.on("tap", stamp)
	r.on("doubleTap", stamp)
	d := newIOSDriver(t, r)
	gap := func(i int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return arrived[i].Sub(arrived[i-1])
	}
	tap := func(x, y int) {
		t.Helper()
		if _, err := d.Tap(ctxT(t), x, y, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
	}

	tap(49, 158)
	tap(49, 158)
	if g := gap(1); g < uikitDoubleTap {
		t.Errorf("a second tap on the same spot went %v after the first: within the double-tap interval (%v), a web view takes the pair as a double tap", g, uikitDoubleTap)
	}
	tap(52, 161)
	if g := gap(2); g < uikitDoubleTap {
		t.Errorf("a tap 3 pt from the last went %v after it; want at least %v", g, uikitDoubleTap)
	}
	if _, err := d.DoubleTap(ctxT(t), 52, 161, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if g := gap(3); g < uikitDoubleTap {
		t.Errorf("a double tap on the spot of the last tap went %v after it (a triple tap); want at least %v", g, uikitDoubleTap)
	}
	tap(52, 161)
	if g := gap(4); g < uikitDoubleTap {
		t.Errorf("a tap on the spot of the last double tap went %v after it; want at least %v", g, uikitDoubleTap)
	}
	tap(300, 700)
	if g := gap(5); g >= uikitDoubleTap {
		t.Errorf("a tap far from the last waited %v; only taps near the last one wait", g)
	}
}

func TestTapSpacingLeavesRepeatsAndOtherGesturesAlone(t *testing.T) {
	const uikitDoubleTap = 350 * time.Millisecond
	r := newFakeRunner(t)
	var mu sync.Mutex
	var arrived []time.Time
	stamp := func(map[string]any) map[string]any {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		return map[string]any{"result": "changed", "seq": 1}
	}
	for _, op := range []string{"tap", "press", "swipe"} {
		r.on(op, stamp)
	}
	d := newIOSDriver(t, r)
	gap := func(i int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return arrived[i].Sub(arrived[i-1])
	}
	tap := func(o driver.ActionOpts) {
		t.Helper()
		if _, err := d.Tap(ctxT(t), 49, 158, o); err != nil {
			t.Fatal(err)
		}
	}

	tap(driver.ActionOpts{})
	tap(driver.ActionOpts{InRepeat: true})
	if g := gap(1); g >= uikitDoubleTap {
		t.Errorf("a repeat's second tap waited %v; the flow's delay times it", g)
	}
	tap(driver.ActionOpts{})
	if g := gap(2); g < uikitDoubleTap {
		t.Errorf("a tap after a repeat went %v after its last tap; want at least %v", g, uikitDoubleTap)
	}
	if _, err := d.LongPress(ctxT(t), 49, 158, 10*time.Millisecond, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	tap(driver.ActionOpts{})
	if g := gap(4); g >= uikitDoubleTap {
		t.Errorf("a tap after a long press waited %v", g)
	}
	if _, err := d.Swipe(ctxT(t), 49, 158, 49, 300, 10*time.Millisecond, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	tap(driver.ActionOpts{})
	if g := gap(6); g >= uikitDoubleTap {
		t.Errorf("a tap after a swipe waited %v", g)
	}
}

func TestTapCancelledWhileSpacedSendsNothing(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 1} })
	d := newIOSDriver(t, r)
	if _, err := d.Tap(ctxT(t), 49, 158, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.Tap(ctx, 49, 158, driver.ActionOpts{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err %v, want the context's", err)
	}
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Errorf("the cancelled tap returned after %v", took)
	}
	if n := len(r.requests("tap")); n != 1 {
		t.Errorf("%d taps sent, want 1 (the cancelled one sends nothing)", n)
	}
}

func TestTapSpacingCountsTheSettleWait(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any {
		return map[string]any{"result": "changed", "seq": 1, "settleMs": int(doubleTapWindow.Milliseconds())}
	})
	d := newIOSDriver(t, r)
	if _, err := d.Tap(ctxT(t), 49, 158, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := d.Tap(ctxT(t), 49, 158, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("a tap after one that settled for the whole window waited %v", took)
	}
}

func TestTapSpacingOnlyInsideAWebView(t *testing.T) {
	const uikitDoubleTap = 350 * time.Millisecond
	r := newFakeRunner(t)
	var mu sync.Mutex
	var arrived []time.Time
	r.on("tap", func(map[string]any) map[string]any {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		return map[string]any{"result": "changed", "seq": 1}
	})
	node := func(class, id string, l, top, right, b int) map[string]any {
		return map[string]any{"c": class, "id": id, "b": []int{l, top, right, b}}
	}
	native := []any{
		node("Button", "nav_push", 20, 184, 82, 218),
		node("Button", "nav_pop", 20, 235, 74, 269),
		node("Button", "nav_tabs", 78, 235, 164, 269),
	}
	web := []any{
		node("Button", "back", 20, 70, 58, 90),
		node("WebView", "", 0, 90, 402, 840),
		node("Button", "", 20, 140, 78, 176),
	}
	var screen []any
	r.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return map[string]any{"seq": 1, "windows": []any{map[string]any{
			"id": 1, "type": 1, "pkg": "dev.testbuddy.bench", "active": true, "b": []int{0, 0, 402, 874}, "nodes": screen}}}
	})
	d := newIOSDriver(t, r)
	show := func(nodes []any) {
		t.Helper()
		mu.Lock()
		screen = nodes
		mu.Unlock()
		if _, err := d.Screen(ctxT(t)); err != nil {
			t.Fatal(err)
		}
	}
	gap := func(i int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return arrived[i].Sub(arrived[i-1])
	}
	tap := func(x, y int) {
		t.Helper()
		if _, err := d.Tap(ctxT(t), x, y, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
	}

	show(native)
	tap(51, 201)
	show(native)
	tap(51, 201)
	if g := gap(1); g >= uikitDoubleTap/2 {
		t.Errorf("a tap on a native button, on the spot of the last tap, waited %v: no web view there to take the pair as a double tap", g)
	}
	show(native)
	tap(47, 252)
	if g := gap(2); g >= uikitDoubleTap/2 {
		t.Errorf("a tap on a native button 51 pt from the last waited %v", g)
	}
	show(native)
	tap(92, 252)
	if g := gap(3); g >= uikitDoubleTap/2 {
		t.Errorf("a tap on a native button 45 pt from the last waited %v", g)
	}

	show(web)
	tap(49, 158)
	show(web)
	tap(49, 158)
	if g := gap(5); g < uikitDoubleTap {
		t.Errorf("a second tap on a web page's button went %v after the first: within the double-tap interval (%v), the web view takes the pair as a double tap (#212)", g, uikitDoubleTap)
	}
	show(web)
	tap(79, 158)
	if g := gap(6); g < uikitDoubleTap {
		t.Errorf("a tap on a web page 30 pt from the last went %v after it; want at least %v", g, uikitDoubleTap)
	}

	show(native)
	tap(51, 201)
	tap(51, 201)
	if g := gap(8); g < uikitDoubleTap {
		t.Errorf("a tap with no read since the last one went %v after it; want at least %v", g, uikitDoubleTap)
	}

	show(web)
	tap(39, 80)
	show(web)
	tap(39, 80)
	if g := gap(10); g >= uikitDoubleTap/2 {
		t.Errorf("a tap on a native button outside the web view waited %v", g)
	}

	maps := []any{node("Map", "", 0, 90, 402, 840)}
	show(maps)
	tap(200, 400)
	show(maps)
	tap(210, 400)
	if g := gap(12); g < uikitDoubleTap {
		t.Errorf("a tap on a map 10 pt from the last went %v after it; want at least %v", g, uikitDoubleTap)
	}
}

func TestTapSpacingInsideASafariView(t *testing.T) {
	const uikitDoubleTap = 350 * time.Millisecond
	r := newFakeRunner(t)
	var mu sync.Mutex
	var arrived []time.Time
	r.on("tap", func(map[string]any) map[string]any {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		return map[string]any{"result": "changed", "seq": 1}
	})
	r.on("tree", func(map[string]any) map[string]any {
		return map[string]any{"seq": 1, "windows": []any{
			map[string]any{"id": -1, "type": 4, "pkg": safariService, "active": true, "b": []int{0, 0, 402, 874},
				"nodes": []any{map[string]any{"c": "Button", "t": "Sign in to account", "b": []int{208, 198, 327, 219}}}},
			map[string]any{"id": 1, "type": 1, "pkg": "dev.testbuddy.bench", "b": []int{0, 0, 402, 874}},
		}}
	})
	d := newIOSDriver(t, r)
	for i := 0; i < 2; i++ {
		if _, err := d.Screen(ctxT(t)); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Tap(ctxT(t), 267, 208, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if g := arrived[1].Sub(arrived[0]); g < uikitDoubleTap {
		t.Errorf("a second tap on a Safari view's button went %v after the first; want at least %v", g, uikitDoubleTap)
	}
}

func TestTapSpacingKeepsTheLatestRead(t *testing.T) {
	d := &Driver{}
	web := &ui.Screen{Nodes: []*ui.Node{{Class: "WebView", Bounds: ui.Rect{L: 0, T: 90, R: 402, B: 840}}}}
	native := &ui.Screen{Nodes: []*ui.Node{{Class: "Button", Bounds: ui.Rect{L: 20, T: 140, R: 78, B: 176}}}}
	d.noteRead(5, native)
	d.noteRead(4, web)
	if d.mayBeWeb(49, 158, 3) {
		t.Error("an older read answering late replaced the latest: the tap is taken for one in a web view")
	}
	d.noteRead(6, web)
	if !d.mayBeWeb(49, 158, 3) {
		t.Error("a newer read did not replace the last one")
	}
	if !d.mayBeWeb(49, 158, 6) {
		t.Error("with no read started after the last tap, a tap should wait (unknown)")
	}
}
