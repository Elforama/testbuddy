package ios

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// Inject the two observed races at the driver boundary, after the runner selected
// a field/button. Only the timing is controlled. XCTest still performs every input.
type readinessSim struct {
	*Driver
	t                  *testing.T
	shift              bool
	refused, delivered int
	wrongField         bool
}

func (d *readinessSim) node(ctx context.Context, id string) (*ui.Node, error) {
	s, err := d.Screen(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range s.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	d.t.Fatalf("fixture does not expose %s", id)
	return nil, nil
}

func (d *readinessSim) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil || o.Target.ID != "anim_target" {
		return d.Driver.Tap(ctx, x, y, o)
	}
	if d.shift {
		d.shift = false
		n, err := d.node(ctx, "anim_shift_open")
		if err != nil {
			return driver.ActionResult{}, err
		}
		events, stop := d.Events()
		defer stop()
		if _, err = d.Driver.Tap(ctx, n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{}); err != nil {
			return driver.ActionResult{}, err
		}
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			n, err = d.node(ctx, "anim_target")
			if err != nil {
				return driver.ActionResult{}, err
			}
			if n.Bounds.T > o.Target.Bounds.T+150 {
				break
			}
			select {
			case <-events:
			case <-deadline.C:
				d.t.Fatal("banner did not displace target")
			}
		}
	}
	result, err := d.Driver.Tap(ctx, x, y, o)
	if result.Withheld != "" {
		d.refused++
	} else if err == nil {
		d.delivered++
	}
	return result, err
}

func (d *readinessSim) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target != nil && o.Target.ID == "field_secret" {
		n, err := d.node(ctx, "field_age")
		if err != nil {
			return driver.ActionResult{}, err
		}
		if _, err = d.Driver.Tap(ctx, n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{}); err != nil {
			return driver.ActionResult{}, err
		}
		active, err := d.node(ctx, "field_age")
		if err != nil {
			return driver.ActionResult{}, err
		}
		if !active.Focused {
			d.t.Fatal("failed to arrange focus on Age before secure input")
		}
		d.wrongField = true
	}
	return d.Driver.TypeText(ctx, text, o)
}

// Uses the installed Buddy Bench app. Run only on a nominated benchmark simulator.
func TestSimulatorBuddyBenchReadiness(t *testing.T) {
	if os.Getenv("TB_IOS_UDID") == "" {
		t.Skip("set TB_IOS_UDID to a simulator with Buddy Bench installed")
	}
	d := openSim(t)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	instrument := &readinessSim{Driver: d, t: t, shift: true}
	run := func(steps string) {
		t.Helper()
		cmds, err := flow.ParseCommands(".", []byte(steps))
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.New(instrument, engine.Options{}).RunCommands(ctx, cmds, "dev.testbuddy.bench", nil); err != nil {
			t.Fatal(err)
		}
	}
	run(`- launchApp: {clearState: true}
- scrollUntilVisible: {element: {id: scenario_animations}}
- tapOn: {id: scenario_animations}
- tapOn: {id: anim_target}
- assertVisible: "Target tapped"
`)
	if instrument.refused != 1 || instrument.delivered != 1 {
		t.Fatalf("stale tap: refused=%d injected=%d", instrument.refused, instrument.delivered)
	}
	run(`- launchApp: {clearState: true}
- scrollUntilVisible: {element: {id: scenario_controls}}
- tapOn: {id: scenario_controls}
- scrollUntilVisible: {element: {id: field_age}, centerElement: true}
- tapOn: {id: field_age}
- inputText: "37"
- tapOn: {id: field_secret}
- inputText: "hunter2"
- hideKeyboard
- assertVisible: "Secret length: 7"
- assertVisible: "Age: 37"
`)
	if !instrument.wrongField {
		t.Fatal("secure input reached the driver without the intended field ID")
	}
	// Exercise the delivery-time guard separately: on this still screen the
	// generation is current, but the supplied touch is over a *different* star.
	run(`- scrollUntilVisible: {element: {id: star_4}, centerElement: true}`)
	other, err := instrument.node(ctx, "star_2")
	if err != nil {
		t.Fatal(err)
	}
	s, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := findByIDForTest(s, "star_4")
	if target == nil {
		t.Fatal("missing fourth star")
	}
	res, err := d.Tap(ctx, other.Bounds.CX(), other.Bounds.CY(), driver.ActionOpts{Target: &driver.ActionTarget{ID: target.ID, Window: s.Windows[target.Window].ID, Seq: s.Seq, Bounds: target.Bounds}})
	if err != nil || res.Withheld != "delivery" {
		t.Fatalf("touch over the wrong star must be withheld: %+v, %v", res, err)
	}
	run(`- assertVisible: "Rating: 0"
- tapOn: {id: star_4}
- assertVisible: "Rating: 4"`)
	// UIKit's action callback confirms the recipient even if its handler does
	// not change the screen. The benchmark's counter detects duplicate input.
	run(`- launchApp: {clearState: true}
- scrollUntilVisible: {element: {id: scenario_native}}
- tapOn: {id: scenario_native}
- tapOn: {id: tk_uikit}
- assertVisible: {id: tk_tap}`)
	s, err = d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	button := findByIDForTest(s, "tk_tap")
	if button == nil {
		t.Fatal("missing UIKit button")
	}
	res, err = d.Tap(ctx, button.Bounds.CX(), button.Bounds.CY(), driver.ActionOpts{Target: &driver.ActionTarget{ID: button.ID, Window: s.Windows[button.Window].ID, Seq: s.Seq, Bounds: button.Bounds}})
	if err != nil || res.Clicked == nil || !*res.Clicked {
		t.Fatalf("UIKit activation receipt: %+v, %v", res, err)
	}
	run(`- assertVisible: "Taps: 1"`)
	// A dead launch PID must fail even though this bundle currently has a readable window.
	err = d.awaitApp(ctx, "dev.testbuddy.bench", time.Second, 2147483647, true)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("dead PID returned %v", err)
	}
}

func findByIDForTest(s *ui.Screen, id string) *ui.Node {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}
