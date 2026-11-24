package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

type switchApp struct {
	f     *fakeDriver
	mu    sync.Mutex
	on    bool
	taps  int
	flips int
	extra *ui.Node
	reply time.Duration

	slowRead time.Duration
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
		a.f.change(true)
		time.Sleep(a.reply)
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

func TestSwitchTapTheAppIgnoredIsSentAgain(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(n int) (bool, time.Duration) { return n > 1, 0 })
	mustPass(t, runWith(t, a.f, quick, switchFlow))
	if taps, flips, on := a.state(); taps != 2 || flips != 1 || !on {
		t.Fatalf("taps %d, flips %d, on %v: want the lost tap sent again once, and the switch on", taps, flips, on)
	}
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
				time.Sleep(c.reply + c.after + 200*time.Millisecond)
				if taps, flips, on := a.state(); taps != 1 || flips != 1 || !on {
					t.Fatalf("taps %d, flips %d, on %v: want one tap and the switch on", taps, flips, on)
				}
			})
		}
	}
}

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

func TestSwitchThatNeverMovesIsTappedAtMostTwice(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(int) (bool, time.Duration) { return false, 0 })
	mustFail(t, runWith(t, a.f, quick, switchFlow), "good_push")
	if taps, _, _ := a.state(); taps != 2 {
		t.Fatalf("taps %d: want 2", taps)
	}
}

func TestAndroidSwitchTapIsNotChecked(t *testing.T) {
	t.Setenv("TB_CI", "1")
	a := newSwitchApp(func(n int) (bool, time.Duration) { return n > 1, 0 })
	a.f.plat = "android"
	mustFail(t, runWith(t, a.f, quick, switchFlow), "good_push")
	if taps, _, _ := a.state(); taps != 1 {
		t.Fatalf("taps %d: want 1", taps)
	}
}
