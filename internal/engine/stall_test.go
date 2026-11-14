package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// stallDriver reports its device stalled the whole time since t (stalled), or not at all.
type stallDriver struct {
	*fakeDriver
	stalled bool
}

func (d *stallDriver) StallSince(t time.Time) driver.Stall {
	st := driver.Stall{Device: "the simulator", Signs: []string{"accessibility timeouts"}}
	if d.stalled {
		st.For = time.Since(t)
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
	return New(d, Options{LookupTimeout: 300 * time.Millisecond}).RunCommands(ctx, cmds, "app", nil)
}

// Issue #237: a step that failed to find its element while the device was not answering says
// so first, once (an enclosing runFlow does not say it again), and keeps its own message; a
// step whose device answered as usual is left as it was.
func TestFailureNamesTheDeviceStall(t *testing.T) {
	screen := func() []*ui.Node {
		return []*ui.Node{{Text: "Other", Visible: true, Bounds: ui.Rect{L: 0, T: 100, R: 100, B: 140}}}
	}
	cases := []struct {
		name, src     string
		stalled       bool
		want, wantNot string
	}{
		{"stalled", "- assertVisible: Save", true,
			"the simulator stopped answering for", ""},
		{"stalled in a runFlow: named once", "- runFlow:\n    commands:\n      - assertVisible: Save", true,
			"the simulator stopped answering for", ""},
		{"answering", "- assertVisible: Save", false, "", "stopped answering"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			f.screen = screen
			err := runStallSteps(t, &stallDriver{fakeDriver: f, stalled: c.stalled}, c.src)
			if err == nil {
				t.Fatal("the step passed")
			}
			msg := err.Error()
			if !strings.Contains(msg, "Save") {
				t.Errorf("the step's own message is gone: %v", msg)
			}
			if c.want != "" && (!strings.Contains(msg, c.want) || strings.Count(msg, "stopped answering") != 1 ||
				!strings.Contains(msg, "(accessibility timeouts)")) {
				t.Errorf("err = %v, want %q once", msg, c.want)
			}
			if c.wantNot != "" && strings.Contains(msg, c.wantNot) {
				t.Errorf("err = %v names a stall", msg)
			}
		})
	}
}
