package engine

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// stallDriver's device stalls on reads: every read while stalling is set (all of them), or
// only the first one (once), each for hold.
type stallDriver struct {
	*fakeDriver
	all, once bool
	hold      time.Duration

	mu     sync.Mutex
	reads  int
	stalls [][2]time.Time
}

func (d *stallDriver) Screen(ctx context.Context) (*ui.Screen, error) {
	d.mu.Lock()
	d.reads++
	stall := d.all || (d.once && d.reads == 1)
	d.mu.Unlock()
	if stall {
		start := time.Now()
		time.Sleep(d.hold)
		d.mu.Lock()
		d.stalls = append(d.stalls, [2]time.Time{start, time.Now()})
		d.mu.Unlock()
	}
	return d.fakeDriver.Screen(ctx)
}

func (d *stallDriver) StallSince(t time.Time) driver.Stall {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := driver.Stall{Device: "the simulator", Stopped: true, Signs: []string{"an accessibility timeout"}}
	for _, s := range d.stalls {
		if s[1].After(t) {
			from := s[0]
			if from.Before(t) {
				from = t
			}
			st.For += s[1].Sub(from)
		}
	}
	return st
}

func runStallSteps(t *testing.T, d driver.Driver, src string) error {
	t.Helper()
	cmds, err := flow.ParseCommands(".", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return New(d, Options{LookupTimeout: 300 * time.Millisecond, StallFloor: 100 * time.Millisecond}).RunCommands(ctx, cmds, "app", nil)
}

// Issue #237: a step that failed to find its element while the device was not answering says
// so first, once (an enclosing runFlow does not say it again), and keeps its own message; a
// step whose device answered as usual is left as it was, and so is one that failed promptly
// after an earlier step in the same runFlow passed through a stall.
func TestFailureNamesTheDeviceStall(t *testing.T) {
	screen := func() []*ui.Node {
		return []*ui.Node{{Text: "Other", Visible: true, Bounds: ui.Rect{L: 0, T: 100, R: 100, B: 140}}}
	}
	named := "the simulator stopped answering for"
	cases := []struct {
		name, src string
		all, once bool
		named     bool
	}{
		{"stalled", "- assertVisible: Save", true, false, true},
		{"stalled in a runFlow: named once", "- runFlow:\n    commands:\n      - assertVisible: Save", true, false, true},
		{"answering", "- assertVisible: Save", false, false, false},
		{"an earlier step in the runFlow stalled", "- runFlow:\n    commands:\n      - assertVisible: Other\n      - assertVisible: Save", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			f.screen = screen
			err := runStallSteps(t, &stallDriver{fakeDriver: f, all: c.all, once: c.once, hold: 400 * time.Millisecond}, c.src)
			if err == nil {
				t.Fatal("the step passed")
			}
			msg := err.Error()
			if !strings.Contains(msg, "Save") {
				t.Errorf("the step's own message is gone: %v", msg)
			}
			if c.named && (strings.Count(msg, named) != 1 || !strings.Contains(msg, "(an accessibility timeout)") ||
				!strings.Contains(msg, "of this step's") || !strings.Contains(msg, "the step's own failure: ")) {
				t.Errorf("err = %v, want the stall named once", msg)
			}
			if !c.named && strings.Contains(msg, "stopped answering") {
				t.Errorf("err = %v names a stall", msg)
			}
		})
	}
}

// Issue #243: in CI mode a find or assert wait whose time a device stall used up gets the
// stalled time back, once, up to its own timeout: the first read stalls past the wait's whole
// timeout, and the screen it is waiting for comes with the next read. Outside CI the step fails
// as before (naming the stall, issue #237). A stall in an earlier step gives a later one nothing,
// and an element that is really missing still fails, saying it had the extra time.
func TestWaitsOutADeviceStallInCI(t *testing.T) {
	save := []*ui.Node{{Text: "Save", Visible: true, Bounds: ui.Rect{L: 0, T: 100, R: 100, B: 140}}}
	other := []*ui.Node{{Text: "Other", Visible: true, Bounds: ui.Rect{L: 0, T: 200, R: 100, B: 240}}}
	cases := []struct {
		name, src  string
		first      []*ui.Node // the stalled first read
		later      []*ui.Node // every later read
		ci, pass   bool
		extraNamed bool // the failure says the step had more time
	}{
		{"assertVisible in CI", "- assertVisible: Save", other, save, true, true, false},
		{"assertVisible outside CI", "- assertVisible: Save", other, save, false, false, false},
		{"assertNotVisible in CI", "- assertNotVisible: Save", save, other, true, true, false},
		{"assertNotVisible outside CI", "- assertNotVisible: Save", save, other, false, false, false},
		{"extendedWaitUntil in CI", "- extendedWaitUntil: {visible: Save, timeout: 300}", other, save, true, true, false},
		{"a stall in an earlier step", "- assertVisible: Other\n- assertVisible: Save", other, other, true, false, false},
		{"a missing element still fails", "- assertVisible: Save", other, other, true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.ci {
				t.Setenv("TB_CI", "1")
			} else {
				t.Setenv("TB_CI", "0")
			}
			f := &fakeDriver{plat: "ios"}
			var mu sync.Mutex
			reads := 0
			f.screen = func() []*ui.Node {
				mu.Lock()
				defer mu.Unlock()
				reads++
				if reads == 1 {
					return c.first
				}
				return c.later
			}
			start := time.Now()
			err := runStallSteps(t, &stallDriver{fakeDriver: f, once: true, hold: 400 * time.Millisecond}, c.src)
			took := time.Since(start)
			if c.pass && err != nil {
				t.Fatalf("the step failed after a stall it could have waited out: %v", err)
			}
			if !c.pass && err == nil {
				t.Fatal("the step passed")
			}
			if err != nil && strings.Contains(err.Error(), "more (CI mode)") != c.extraNamed {
				t.Errorf("err = %v: the extra time named wrongly", err)
			}
			// The stall, the wait's timeout and at most one stall's length (300 ms) more.
			if took > 2*time.Second {
				t.Errorf("took %s", took)
			}
		})
	}
}

// stallScreens is a fakeDriver whose first read shows first (the stalled one, with
// stallDriver{once: true}) and every later read later.
func stallScreens(first, later []*ui.Node) *fakeDriver {
	f := &fakeDriver{plat: "ios"}
	var mu sync.Mutex
	reads := 0
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if reads == 1 {
			return first
		}
		return later
	}
	return f
}

func runStallTraced(t *testing.T, d driver.Driver, src string) (string, error) {
	t.Helper()
	cmds, err := flow.ParseCommands(".", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var buf bytes.Buffer
	err = New(d, Options{LookupTimeout: 300 * time.Millisecond, StallFloor: 100 * time.Millisecond, Trace: NewTrace(&buf)}).
		RunCommands(ctx, cmds, "app", nil)
	return buf.String(), err
}

// Issue #243 (review): only a stall inside the wait's own time counts. Here the condition's
// first wait (visible: Other) stalls and passes; its second (notVisible: Save) then runs out on
// a device that answers as usual, and gets nothing: it lost no time.
func TestStallBeforeTheWaitGivesItNothing(t *testing.T) {
	t.Setenv("TB_CI", "1")
	both := []*ui.Node{
		{Text: "Other", Visible: true, Bounds: ui.Rect{L: 0, T: 200, R: 100, B: 240}},
		{Text: "Save", Visible: true, Bounds: ui.Rect{L: 0, T: 100, R: 100, B: 140}},
	}
	trace, err := runStallTraced(t, &stallDriver{fakeDriver: stallScreens(both, both), once: true, hold: 400 * time.Millisecond},
		"- extendedWaitUntil: {visible: Other, notVisible: Save, timeout: 300}")
	if err == nil {
		t.Fatal("the step passed")
	}
	if strings.Contains(trace, "more, once (CI mode)") || strings.Contains(err.Error(), "more (CI mode)") {
		t.Errorf("a wait was given time for a stall before it began: %v\n%s", err, trace)
	}
}

// Issue #243 (review): a step inside a runFlow gets its own single extension; the runFlow
// around it gets none of its own, and the next step none.
func TestNestedStepGetsItsOwnExtension(t *testing.T) {
	t.Setenv("TB_CI", "1")
	save := []*ui.Node{{Text: "Save", Visible: true, Bounds: ui.Rect{L: 0, T: 100, R: 100, B: 140}}}
	other := []*ui.Node{{Text: "Other", Visible: true, Bounds: ui.Rect{L: 0, T: 200, R: 100, B: 240}}}
	trace, err := runStallTraced(t, &stallDriver{fakeDriver: stallScreens(other, save), once: true, hold: 400 * time.Millisecond},
		"- runFlow:\n    commands:\n      - assertVisible: Save\n      - assertVisible: Missing")
	if err == nil || strings.Contains(err.Error(), "more (CI mode)") {
		t.Fatalf("err = %v, want the second step's plain failure", err)
	}
	if n := strings.Count(trace, "more, once (CI mode)"); n != 1 {
		t.Errorf("given more time %d times, want once (the first nested step):\n%s", n, trace)
	}
}
