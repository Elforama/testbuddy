package engine

import (
	"context"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// Android may send TYPE_VIEW_CLICKED after the action reply but before recovery
// starts. Once that event is known, rereading and settling the tree cannot justify
// another tap. Everyday's Compose buttons used to pay for those reads after each click.
func TestLateAndroidClickSkipsRecoveryReads(t *testing.T) {
	f := &fakeDriver{plat: "android", screen: static(btn("Target", "target", 100))}
	f.clicks.Store(1)
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	before := int64(0)
	again, err := r.tapMovedTarget(context.Background(), sel, btn("Target", "target", 100), nil, 200, 125, &before, driver.ActionOpts{}, true)
	if err != nil || again {
		t.Fatalf("recovery = %v, %v", again, err)
	}
	if f.reads.Load() != 0 || f.waits.Load() != 0 {
		t.Fatalf("confirmed click performed %d reads and %d settle waits", f.reads.Load(), f.waits.Load())
	}
}

func TestAndroidTapStateConfirmsDeliveryWithoutMotionRecovery(t *testing.T) {
	for _, kind := range []string{"checkbox", "field", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			was := btn("Target", "target", 100)
			now := btn("Target", "target", 100)
			if kind == "checkbox" {
				was.Checkable = true
				now.Checkable = true
				now.Checked = true
			} else if kind == "field" {
				was.Editable = true
				now.Editable = true
				now.Focused = true
			} else {
				now.Enabled = false
			}
			f := &fakeDriver{plat: "android", screen: static(was)}
			before, err := f.Screen(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			f.screen = static(now)
			f.reads.Store(0)
			r := New(f, quick)
			sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
			if err != nil {
				t.Fatal(err)
			}
			count := int64(0)
			again, err := r.tapMovedTarget(context.Background(), sel, was, before, 200, 125, &count, driver.ActionOpts{}, true)
			if err != nil || again {
				t.Fatalf("recovery = %v, %v", again, err)
			}
			if f.reads.Load() != 1 || f.waits.Load() != 0 {
				t.Fatalf("delivery proof required %d reads and %d waits", f.reads.Load(), f.waits.Load())
			}
		})
	}
}

// A newly disabled control displaced off the injected point is still ambiguous:
// a layout shift may have caused the original touch to miss it.
func TestDisabledDisplacedTargetStillTakesRecoveryChecks(t *testing.T) {
	was := btn("Target", "target", 100)
	now := btn("Target", "target", 300)
	now.Enabled = false
	f := &fakeDriver{plat: "android", screen: static(was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.screen = static(now)
	f.reads.Store(0)
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	_, err = r.tapMovedTarget(context.Background(), sel, was, before, 200, 125, &count, driver.ActionOpts{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if f.reads.Load() <= 1 || f.waits.Load() == 0 {
		t.Fatalf("displaced target skipped recovery: reads=%d waits=%d", f.reads.Load(), f.waits.Load())
	}
}

// A navigation action removes its own button. Recovery should inspect the settled
// result once, without spending a lookup timeout searching the screen it left.
func TestAndroidRecoveryDoesNotWaitForRemovedNavigationButton(t *testing.T) {
	was := btn("Menu", "menu", 100)
	f := &fakeDriver{plat: "android", screen: static(was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.screen = static(btn("Help", "help", 300))
	f.reads.Store(0)
	r := New(f, quick)
	r.unsettled = time.Second
	sel, err := r.resolveSelector(&flow.Selector{ID: "menu"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 125, &count, driver.ActionOpts{}, true)
	if err != nil || again {
		t.Fatalf("recovery = %v,%v", again, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("recovery exhausted its context waiting for a removed button: %v", ctx.Err())
	}
	if f.reads.Load() != 1 {
		t.Fatalf("removed button caused %d recovery reads", f.reads.Load())
	}
}

// Different routes can expose an identically shaped anonymous ScrollView. Once
// a tap removes its target and changes the screen context, it must not scroll
// the new route trying to recover a control from the old one.
func TestAndroidRecoveryDoesNotScrollANewAnonymousList(t *testing.T) {
	was := &ui.Node{Depth: 1, Class: "Button", ID: "scene-still", Text: "Still", Clickable: true,
		Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}
	f := &fakeDriver{plat: "android", screen: static(
		&ui.Node{ID: "heading", Text: "Choose scene", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.screen = static(
		&ui.Node{ID: "heading", Text: "Still scene", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		&ui.Node{Depth: 1, Text: "Nothing moves", Bounds: ui.Rect{T: 100, R: 400, B: 150}})
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "scene-still"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 425, &count, driver.ActionOpts{}, true)
	if err != nil || again || len(f.swipeAt) != 0 || ctx.Err() != nil {
		t.Fatalf("recovered an old route's control: again=%v error=%v swipes=%v context=%v", again, err, f.swipeAt, ctx.Err())
	}
}

// Navigation can commit after the first post-tap read. The context boundary
// must also apply to the later read that reports the old target missing.
func TestAndroidRecoveryDoesNotScrollAfterDelayedRouteChange(t *testing.T) {
	was := &ui.Node{Depth: 1, Class: "Button", ID: "key-4", Text: "4", Clickable: true,
		Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}
	old := static(
		&ui.Node{Text: "Security", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, was)
	f := &fakeDriver{screen: old}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.screen = func() []*ui.Node {
		reads++
		f.seq++
		if reads == 1 {
			return old()
		}
		return static(
			&ui.Node{Text: "Profile", Bounds: ui.Rect{R: 400, B: 50}},
			&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}})()
	}
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "key-4"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 425, &count, driver.ActionOpts{}, true)
	if err != nil || again || len(f.swipeAt) != 0 || ctx.Err() != nil {
		t.Fatalf("recovered after delayed navigation: again=%v error=%v swipes=%v context=%v", again, err, f.swipeAt, ctx.Err())
	}
}

// AND-001: the keypad can disappear only after stabilization has already
// observed it. Keeping the last-seen node must not hide the new route.
func TestRecoveryStopsWhenRouteChangesDuringStabilization(t *testing.T) {
	was := &ui.Node{Depth: 1, Class: "Button", ID: "key-4", Text: "4", Clickable: true,
		Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}
	old := static(
		&ui.Node{Text: "Security", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, was)
	f := &fakeDriver{screen: old}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.screen = func() []*ui.Node {
		reads++
		f.seq++
		if reads <= 3 {
			return old()
		}
		return static(
			&ui.Node{Text: "Profile", Bounds: ui.Rect{R: 400, B: 50}},
			&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}})()
	}
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "key-4"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 425, &count, driver.ActionOpts{Timeout: 100 * time.Millisecond}, true)
	if err != nil || again || len(f.swipeAt) != 0 || ctx.Err() != nil {
		t.Fatalf("recovered an obsolete keypad: again=%v error=%v swipes=%v context=%v", again, err, f.swipeAt, ctx.Err())
	}
}

// Focus retained from before a tap cannot prove that the tap reached a field
// with a custom click handler. Keep the ordinary motion checks in that case.
func TestAlreadyFocusedFieldStillTakesRecoveryChecks(t *testing.T) {
	was := btn("Target", "target", 100)
	was.Editable, was.Focused = true, true
	f := &fakeDriver{plat: "android", screen: static(was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.reads.Store(0)
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	_, err = r.tapMovedTarget(context.Background(), sel, was, before, 200, 125, &count, driver.ActionOpts{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if f.waits.Load() == 0 {
		t.Fatal("existing focus was mistaken for a new interaction result")
	}
}
