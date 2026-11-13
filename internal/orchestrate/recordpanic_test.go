package orchestrate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
)

// issue #107: with --record, a flow that crashes (a panic, recovered by runSafely) stops
// every recording it started, exactly once, and its result (what the console, JSON, HTML
// and JUnit reports list) still has the videos, as a failure naming the crash. Single-device
// flows left the recording running; neither kind kept its videos in the result.
func TestScheduleRecordedPanicStopsRecordingsAndKeepsVideos(t *testing.T) {
	for _, c := range []struct {
		name  string
		yaml  string
		needs []Need
		roles []string // the videos' roles, in order
	}{
		{name: "single device", yaml: "appId: com.app\n---\n- launchApp\n- inputText: x\n",
			needs: []Need{{}}, roles: []string{""}},
		{name: "multi device", yaml: twoRoles + "- alice: [launchApp]\n- bob: [{inputText: x}]\n",
			needs: []Need{{Role: "alice"}, {Role: "bob"}}, roles: []string{"alice", "bob"}},
		// a role of a parallel: block runs on its own goroutine: its crash ended the process
		{name: "parallel block", yaml: twoRoles + "- parallel:\n    alice: [launchApp]\n    bob: [{inputText: x}]\n",
			needs: []Need{{Role: "alice"}, {Role: "bob"}}, roles: []string{"alice", "bob"}},
		// onFlowStart runs on every role at once, as a parallel: block does
		{name: "multi-device onFlowStart", yaml: "appId: com.app\ndevices:\n  alice: android\n  bob: android\nonFlowStart:\n  - inputText: x\n---\n- alice: [launchApp]\n",
			needs: []Need{{Role: "alice"}, {Role: "bob"}}, roles: []string{"alice", "bob"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := newFake("a", "Phone A", "android"), newFake("b", "Phone B", "android")
			crash := func(string) { panic("typing crashed") }
			a.onType, b.onType = crash, crash
			h := newRecorder(t).hooks()
			opts := h.Options
			h.Options = func(j *Job, devs map[string]device.Info) engine.Options {
				o := opts(j, devs)
				o.Record = true
				return o
			}
			pool := poolOf(a, b)
			defer pool.Close()
			res := scheduleNoPanic(t, pool, []*Job{{Flow: parseFlow(t, "crash", c.yaml), Needs: c.needs}}, 0, false, h)
			if len(res) != 1 {
				t.Fatalf("%d results, want 1", len(res))
			}
			r := res[0]
			if r.Status != engine.Failed || r.Err == nil || !strings.Contains(r.Err.Error(), "typing crashed") ||
				!strings.Contains(r.Err.Error(), "tb crashed running this flow") {
				t.Errorf("result %s %v, want failed with the crash", r.Status, r.Err)
			}
			for _, d := range []*fakeDriver{a, b} {
				d.mu.Lock()
				started, stopped := len(d.recStart), d.recStop
				d.mu.Unlock()
				if started != stopped {
					t.Errorf("%s: recordings started %d, stopped %d", d.name, started, stopped)
				}
			}
			var roles []string
			for _, v := range r.Videos {
				roles = append(roles, v.Role)
			}
			if !reflect.DeepEqual(roles, c.roles) {
				t.Errorf("videos of roles %q, want %q", roles, c.roles)
			}
		})
	}
}
