package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// switchApp is the bench's accessibility screen on iOS (issue #250): a status line and a
// SwiftUI Toggle, read as a Switch whose text and value are "0" or "1", with its control inside.
// take decides, per tap (from 1), whether the app takes it, and after how long it shows the
// switch flipped. Every tap redraws something unlisted at once (the press's highlight), so
// the agent reports "the screen changed" whether or not the switch moved, as in #250.
type switchApp struct {
	f     *fakeDriver
	mu    sync.Mutex
	on    bool
	taps  int
	flips int
	extra *ui.Node      // something the tap opened (a sheet's text), if set
	reply time.Duration // how long each tap's reply takes (a slow agent or app); the flip is timed from it

	slowRead time.Duration // how long the first read after the first tap takes, if set
	slowDone bool
}

func newSwitchApp(take func(n int) (bool, time.Duration)) *switchApp {
	a := &switchApp{f: &fakeDriver{plat: "ios"}}
	a.f.screen = func() []*ui.Node {
		a.mu.Lock()
		defer a.mu.Unlock()
		v := map[bool]string{false: "0", true: "1"}[a.on]
		status := "Quantity 1"
		if a.flips > 0 {
			status = map[bool]string{false: "Push off", true: "Push on"}[a.on]
		}
		nodes := []*ui.Node{
			{Class: "StaticText", ID: "good_result", Text: status, Enabled: true, Bounds: ui.Rect{L: 20, T: 600, R: 300, B: 620}},
			{Class: "Switch", ID: "good_push", Desc: "Push notifications", Text: v, Value: v, Checkable: true, Checked: a.on,
				Clickable: true, Enabled: true, Bounds: ui.Rect{L: 20, T: 772, R: 81, B: 800}},
			{Class: "Switch", Depth: 1, Text: v, Value: v, Checkable: true, Checked: a.on, Clickable: true, Enabled: true,
				Bounds: ui.Rect{L: 20, T: 772, R: 83, B: 800}},
		}
		if a.extra != nil {
			nodes = append(nodes, a.extra)
		}
		if a.taps == 1 && a.slowRead > 0 && !a.slowDone {
			// The first read after the tap is slow, and shows the switch as it was when it
			// started (the app busy): a.mu is released before the wait, so the flip can come.
			a.slowDone = true
			a.mu.Unlock()
			time.Sleep(a.slowRead)
			a.mu.Lock()
		}
		return nodes
	}
	a.f.onTap = func(_, _ int) {
		a.mu.Lock()
		a.taps++
		n := a.taps
		a.mu.Unlock()
		a.f.change(true)    // the press's own redraw
		time.Sleep(a.reply) // the agent's reply comes this long after the touch went out
		took, after := take(n)
		if !took {
			return
		}
		flip := func() {
			a.mu.Lock()
			a.on = !a.on
			a.flips++
			a.mu.Unlock()
			a.f.change(true)
		}
		if after == 0 {
			flip()
		} else {
			go func() { time.Sleep(after); flip() }()
		}
	}
	return a
}

func (a *switchApp) state() (taps, flips int, on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.taps, a.flips, a.on
}

const switchFlow = "- tapOn: Push notifications\n- assertVisible: {id: good_push, checked: true}"

// Issue #250: a tap on an iOS switch was delivered (testmanagerd played it, the app answered)
// but the switch did not toggle, and tb reported "the screen changed" (another redraw) and
// passed the tap: the assertion after it failed. A UISwitch (a SwiftUI Toggle) ignores a short
// touch whose down and up the app gets in one batch, as when its main thread is busy across the
// 20 ms touch (measured: 17 of 20 such taps lost on an iOS 27 simulator; buttons take them).
// In CI mode the tap on a switch is checked by the switch's value: one that did not move, with
// nothing else on screen changed, is tapped once more. Outside CI mode the tap stands, as
// before (seen in CI only; a local simulator lost none in 65 runs, idle or loaded).
func TestSwitchTapTheAppIgnoredIsSentAgain(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(n int) (bool, time.Duration) { return n > 1, 0 })
	mustPass(t, runWith(t, a.f, quick, switchFlow))
	if taps, flips, on := a.state(); taps != 2 || flips != 1 || !on {
		t.Fatalf("taps %d, flips %d, on %v: want the lost tap sent again once, and the switch on", taps, flips, on)
	}
	// The second touch is held long enough that a stall still going on cannot batch it.
	if h := []time.Duration{a.f.tapOpts[0].Hold, a.f.tapOpts[1].Hold}; h[0] != iosFrameHold || h[1] != switchRetryHold {
		t.Fatalf("holds %v: want %s, then %s", h, iosFrameHold, switchRetryHold)
	}

	t.Setenv("TB_CI", "0")
	a = newSwitchApp(func(n int) (bool, time.Duration) { return n > 1, 0 })
	mustFail(t, runWith(t, a.f, quick, switchFlow), "good_push")
	if taps, _, _ := a.state(); taps != 1 {
		t.Fatalf("TB_CI=0: taps %d: want 1 (no check outside CI mode)", taps)
	}
}

// A switch tap that took is never sent again, however late its value shows within the wait,
// so the switch is never toggled twice (issue #250). That holds after a slow tap reply too:
// the wait runs from the reply as well as from the touch (a reply after 1.1 s with the flip
// 100 ms later, or after 900 ms with the flip at 1050 ms, was tapped again and toggled back).
func TestSwitchTapThatTookIsNotSentAgain(t *testing.T) {
	type tc struct{ reply, after time.Duration }
	cases := []tc{{0, 0}, {0, 150 * time.Millisecond}, {0, 400 * time.Millisecond}, {0, 800 * time.Millisecond},
		{1100 * time.Millisecond, 100 * time.Millisecond}, {900 * time.Millisecond, 150 * time.Millisecond}}
	for _, mode := range []string{"1", "0"} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("TB_CI=%s/reply %s/flip %s after", mode, c.reply, c.after), func(t *testing.T) {
				t.Setenv("TB_CI", mode)
				a := newSwitchApp(func(int) (bool, time.Duration) { return true, c.after })
				a.reply = c.reply
				o := quick
				o.LookupTimeout = 2 * time.Second
				mustPass(t, runWith(t, a.f, o, switchFlow))
				time.Sleep(c.reply + c.after + 200*time.Millisecond) // a second tap's flip would have landed
				if taps, flips, on := a.state(); taps != 1 || flips != 1 || !on {
					t.Fatalf("taps %d, flips %d, on %v: want one tap and the switch on", taps, flips, on)
				}
			})
		}
	}
}

// A read that took over a second (the app busy) past the end of the wait gives the switch a
// little longer, once: what it showed was the screen as the read started (issue #250).
func TestSwitchTapAfterASlowReadIsNotSentAgain(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(int) (bool, time.Duration) { return true, 1100 * time.Millisecond })
	a.slowRead = 1200 * time.Millisecond
	o := quick
	o.LookupTimeout = 2 * time.Second
	mustPass(t, runWith(t, a.f, o, switchFlow))
	time.Sleep(1500 * time.Millisecond)
	if taps, flips, on := a.state(); taps != 1 || flips != 1 || !on {
		t.Fatalf("taps %d, flips %d, on %v: want one tap and the switch on", taps, flips, on)
	}
}

// A switch tap whose switch did not move but that changed something else on screen (a sheet
// asking to confirm, a permission prompt in the app) took effect: it is not sent again.
func TestSwitchTapThatChangedTheScreenIsNotSentAgain(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(int) (bool, time.Duration) { return false, 0 })
	inner := a.f.onTap
	a.f.onTap = func(x, y int) {
		a.mu.Lock()
		a.extra = &ui.Node{Class: "StaticText", ID: "confirm", Text: "Allow notifications?", Enabled: true, Bounds: ui.Rect{L: 20, T: 300, R: 380, B: 340}}
		a.mu.Unlock()
		inner(x, y)
	}
	mustPass(t, runWith(t, a.f, quick, "- tapOn: Push notifications\n- assertVisible: Allow notifications?"))
	if taps, _, _ := a.state(); taps != 1 {
		t.Fatalf("taps %d: want one (the tap opened something)", taps)
	}
}

// A switch the app never lets move (it turns itself back, or ignores every touch) is tapped
// at most twice: the step after it decides.
func TestSwitchThatNeverMovesIsTappedAtMostTwice(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(int) (bool, time.Duration) { return false, 0 })
	mustFail(t, runWith(t, a.f, quick, switchFlow), "good_push")
	if taps, _, _ := a.state(); taps != 2 {
		t.Fatalf("taps %d: want 2", taps)
	}
}

// Android switches report their click: the check is iOS's only (issue #250).
func TestAndroidSwitchTapIsNotChecked(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(n int) (bool, time.Duration) { return n > 1, 0 })
	a.f.plat = "android"
	mustFail(t, runWith(t, a.f, quick, switchFlow), "good_push")
	if taps, _, _ := a.state(); taps != 1 {
		t.Fatalf("taps %d: want 1", taps)
	}
}
