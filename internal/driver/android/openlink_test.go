package android

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

// linkDevice is a fake device for openLink: `am start` answers with out at once, and the
// screen changes to "Depth 3" (a pushed change) after drawAfter.
type linkDevice struct {
	mu     sync.Mutex
	opened bool
}

func (l *linkDevice) agent(t *testing.T, out string, drawAfter time.Duration) *fakeAgent {
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		if !strings.Contains(fmt.Sprint(req["cmd"]), "am start") {
			return map[string]any{"code": 0, "out": ""}
		}
		time.AfterFunc(drawAfter, func() {
			l.mu.Lock()
			l.opened = true
			l.mu.Unlock()
			a.push(map[string]any{"ev": "changed", "seq": 1, "pkg": "dev.bench"})
		})
		return map[string]any{"code": 0, "out": out}
	})
	a.on("tree", func(map[string]any) map[string]any {
		l.mu.Lock()
		text, seq := "Scenarios", 0
		if l.opened {
			text, seq = "Depth 3", 1
		}
		l.mu.Unlock()
		return map[string]any{"seq": seq, "windows": []any{map[string]any{
			"id": 7, "type": 1, "active": true, "focused": true, "pkg": "dev.bench", "b": []int{0, 0, 1080, 2400},
			"nodes": []any{map[string]any{"d": 0, "b": []int{0, 0, 1080, 200}, "c": "android.widget.TextView", "t": text, "f": 4098}},
		}}}
	})
	// Nothing is pending when asked: the change the link makes has not started yet.
	a.on("quiet", func(map[string]any) map[string]any { return map[string]any{"quiet": true, "seq": 0} })
	a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": "dev.bench"} })
	return a
}

// The bench navigation-deep-link flow's steps after its openLink, on a real runner.
const linkFlow = `appId: dev.bench
---
- openLink: "bench://level/3"
- runFlow:
    when: {notVisible: "Depth 3", timeout: 0}
    commands:
      - tapOn: {text: "^Open$", optional: true}
`

func runLinkFlow(t *testing.T, d *Driver) engine.FlowResult {
	t.Helper()
	fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "flow.yaml"), []byte(linkFlow))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return engine.New(d, engine.Options{OptionalTimeout: 300 * time.Millisecond, LookupTimeout: time.Second}).RunFlow(ctx, fl)
}

// A link delivered to the activity already on top (a single-activity app handling it in
// onNewIntent), or to a task brought to the front, makes `am start -W` return at once, before
// the app draws what the link shows (issue #194). The step after openLink must still see it:
// here the screen changes 200 ms after `am start` returned.
func TestOpenLinkDeliveredToTopWaitsForTheChange(t *testing.T) {
	for _, out := range []string{
		"Starting: Intent { act=android.intent.action.VIEW dat=bench://level/... pkg=dev.bench }\n" +
			"Warning: Activity not started, intent has been delivered to currently running top-most instance.\n" +
			"Status: ok\nLaunchState: UNKNOWN (0)\nActivity: dev.bench/.MainActivity\nTotalTime: 0\nWaitTime: 3\nComplete\n",
		"Starting: Intent { act=android.intent.action.VIEW dat=bench://level/... pkg=dev.bench }\n" +
			"Warning: Activity not started, its current task has been brought to the front\n" +
			"Status: ok\nLaunchState: UNKNOWN (0)\nActivity: dev.bench/.MainActivity\nTotalTime: 0\nWaitTime: 3\nComplete\n",
	} {
		l := &linkDevice{}
		d := newDriver(t, l.agent(t, out, 200*time.Millisecond))
		res := runLinkFlow(t, d)
		if res.Err != nil || res.Status != engine.Passed {
			t.Fatalf("flow %s: %v", res.Status, res.Err)
		}
		if len(res.Steps) < 2 || res.Steps[1].Status != engine.Skipped {
			t.Errorf("%q\nthe step after openLink read the screen from before the link: steps %+v", out[strings.Index(out, "Warning"):strings.Index(out, "Status")], res.Steps)
		}
	}
}

// A link to a screen that changes nothing costs only the short wait for a change to start.
func TestOpenLinkDeliveredToTopNoChange(t *testing.T) {
	l := &linkDevice{}
	out := "Warning: Activity not started, intent has been delivered to currently running top-most instance.\nStatus: ok\nComplete\n"
	d := newDriver(t, l.agent(t, out, time.Hour))
	start := time.Now()
	if err := d.OpenLink(ctxT(t), "bench://level/3", "dev.bench", false, false); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < linkReact()-50*time.Millisecond || took > linkReact()+time.Second {
		t.Errorf("OpenLink took %s, want about %s", took, linkReact())
	}
}
