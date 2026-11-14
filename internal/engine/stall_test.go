package engine

import (
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
