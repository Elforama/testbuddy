package ios

// An end-to-end check on a booted simulator, with timings. It drives Settings, so it is off by
// default: TB_IOS_SIM=1 go test ./internal/driver/ios -run Simulator -v
// (TB_IOS_UDID picks the simulator; the first booted one otherwise).

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func openSim(t *testing.T) *Driver {
	t.Helper()
	if os.Getenv("TB_IOS_SIM") != "1" {
		t.Skip("set TB_IOS_SIM=1 to run against a booted simulator")
	}
	ctx := context.Background()
	info, err := device.Pick(device.List(ctx), device.IOS, os.Getenv("TB_IOS_UDID"))
	if err != nil {
		t.Skip(err)
	}
	t0 := time.Now()
	d, err := Open(ctx, info)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("connected to %s (port %d, build %s) in %s", info, d.Port(), BuildID(), time.Since(t0).Round(time.Millisecond))
	return d
}

func find(s *ui.Screen, text string, classes ...string) *ui.Node {
	for _, n := range s.Nodes {
		if n.Label() != text && n.Text != text {
			continue
		}
		if len(classes) == 0 {
			return n
		}
		for _, c := range classes {
			if n.Class == c {
				return n
			}
		}
	}
	return nil
}

// tapTimed taps a node and reports the action's own timings plus when the host saw the first
// "changed" and the "settled" events.
func tapTimed(t *testing.T, d *Driver, n *ui.Node) driver.ActionResult {
	t.Helper()
	events, stop := d.Events()
	type stamp struct {
		e  driver.Event
		at time.Duration
	}
	var got []stamp
	done, quit := make(chan struct{}), make(chan struct{})
	t0 := time.Now()
	go func() {
		defer close(done)
		for {
			select {
			case e := <-events:
				got = append(got, stamp{e, time.Since(t0)})
			case <-quit: // stop does not close the channel
				return
			}
		}
	}()
	r, err := d.Tap(context.Background(), n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	total := time.Since(t0)
	time.Sleep(20 * time.Millisecond)
	stop()
	close(quit)
	<-done
	var firstChanged, settled time.Duration
	for _, g := range got {
		if g.e.Kind == "changed" && firstChanged == 0 {
			firstChanged = g.at
		}
		if g.e.Kind == "settled" && g.e.Seq == r.Seq && settled == 0 {
			settled = g.at
		}
	}
	t.Logf("tap %-14q -> %-9s inject %3d ms, settle %4d ms, round trip %4d ms (first changed event %s, settled event %s)",
		n.Label(), r.Settle, r.InjectMs, r.SettleMs, total.Milliseconds(), firstChanged.Round(time.Millisecond), settled.Round(time.Millisecond))
	return r
}

func TestSimulatorSettings(t *testing.T) {
	d := openSim(t)
	defer d.Close()
	ctx := context.Background()

	t0 := time.Now()
	if err := d.LaunchApp(ctx, "com.apple.Preferences", driver.LaunchOpts{StopApp: true}); err != nil {
		t.Fatal(err)
	}
	t.Logf("launchApp (stop, permissions, simctl launch, awaitPackage) %s", time.Since(t0).Round(time.Millisecond))
	t0 = time.Now()
	if err := d.WaitSettled(ctx, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	t.Logf("first settle after launch %s", time.Since(t0).Round(time.Millisecond))

	var reads []time.Duration
	var s *ui.Screen
	for i := 0; i < 10; i++ {
		t0 := time.Now()
		var err error
		if s, err = d.Screen(ctx); err != nil {
			t.Fatal(err)
		}
		reads = append(reads, time.Since(t0))
	}
	sort.Slice(reads, func(i, j int) bool { return reads[i] < reads[j] })
	t.Logf("screen read: %d windows, %d nodes, fg %s; median %s, min %s, max %s",
		len(s.Windows), len(s.Nodes), s.Foreground(), reads[len(reads)/2].Round(100*time.Microsecond),
		reads[0].Round(100*time.Microsecond), reads[len(reads)-1].Round(100*time.Microsecond))
	if s.Foreground() != "com.apple.Preferences" {
		t.Fatalf("foreground is %q", s.Foreground())
	}

	for round := 0; round < 3; round++ {
		general := find(s, "General", "Cell", "Button", "StaticText")
		if general == nil {
			t.Fatal("no General row on Settings' first screen")
		}
		r := tapTimed(t, d, general)
		if r.Settle != driver.Changed {
			t.Errorf("tapping General: %s", r.Settle)
		}
		s, _ = d.Screen(ctx)
		if find(s, "About") == nil {
			t.Fatal("General did not open (no About row)")
		}
		back := find(s, "Settings", "Button")
		if back == nil {
			t.Fatal("no back button")
		}
		tapTimed(t, d, back)
		s, _ = d.Screen(ctx)
	}

	// A tap on nothing: no change, decided by the app reporting idle and the no-change window.
	r := tapTimed(t, d, &ui.Node{Text: "(empty area)", Bounds: ui.Rect{L: 0, T: d.h - 6, R: 10, B: d.h - 4}})
	if r.Settle != driver.NoChange {
		t.Logf("tap on an empty area reported %s", r.Settle)
	}
}

// TestSimulatorFixture drives agents/ios's TBFixture app (agents/ios/build.sh --fixture builds
// it; TB_IOS_FIXTURE=<path to TBFixture.app> installs it): typing, the keyboard window,
// hideKeyboard, and a change made 1 s after a tap, with no input, arriving as a pushed event.
func TestSimulatorFixture(t *testing.T) {
	d := openSim(t)
	defer d.Close()
	ctx := context.Background()
	const app = "dev.testbuddy.fixture"
	if p := os.Getenv("TB_IOS_FIXTURE"); p != "" {
		if err := d.InstallApp(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.LaunchApp(ctx, app, driver.LaunchOpts{StopApp: true}); err != nil {
		t.Skipf("the fixture app is not installed (%v)", err)
	}
	d.WaitSettled(ctx, 10*time.Second)
	s, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := func(s *ui.Screen, id string) *ui.Node {
		for _, n := range s.Nodes {
			if n.ID == id {
				return n
			}
		}
		return nil
	}

	// A change with no input: the label changes 1 s after the tap.
	later := find(s, "Change text later", "Button")
	before := byID(s, "status").Label()
	events, stop := d.Events()
	r := tapTimed(t, d, later)
	// The host's way to wait: re-read only when a "changed" event arrives.
	t0 := time.Now()
	reads := 0
	var after string
	deadline := time.After(3 * time.Second)
	for after == "" {
		select {
		case e := <-events:
			if e.Kind != "changed" || e.Seq <= r.Seq {
				continue
			}
			s, _ = d.Screen(ctx)
			reads++
			if l := byID(s, "status").Label(); l != before {
				after = l
			}
		case <-deadline:
			t.Fatal("no changed event brought the delayed label change")
		}
	}
	stop()
	t.Logf("label changed with no input %s after the tap returned (%q -> %q), seen after %d event-triggered reads",
		time.Since(t0).Round(time.Millisecond), before, after, reads)

	// Typing, the keyboard window, hideKeyboard.
	field := byID(s, "field")
	tapTimed(t, d, field)
	s, _ = d.Screen(ctx)
	if !s.Keyboard {
		t.Fatal("no keyboard after tapping the field")
	}
	t0 = time.Now()
	if _, err := d.TypeText(ctx, "hello world", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	typed := time.Since(t0)
	t0 = time.Now()
	if _, err := d.EraseText(ctx, 6, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	erased := time.Since(t0)
	s, _ = d.Screen(ctx)
	if v := byID(s, "field").Value; v != "hello" {
		t.Errorf("field shows %q, want %q", v, "hello")
	}
	t0 = time.Now()
	if err := d.HideKeyboard(ctx); err != nil {
		t.Fatal(err)
	}
	hid := time.Since(t0)
	s, _ = d.Screen(ctx)
	if s.Keyboard {
		t.Error("keyboard still shown")
	}
	t.Logf("type 11 chars %s, erase 6 %s, hideKeyboard %s", typed.Round(time.Millisecond), erased.Round(time.Millisecond), hid.Round(time.Millisecond))
}
