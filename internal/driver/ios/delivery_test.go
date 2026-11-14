package ios

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// A runner may withhold a tap it can prove would not reach the app ("preflight": the target
// moved before the touch; "delivery": the touch landed on something else). tb trusts that
// receipt only where it can be true: a guarded tap on a named target, with no click reported.
// Anything else is a broken receipt and an error, never a silent "nothing happened".
func TestTapDeliveryReceipt(t *testing.T) {
	target := &driver.ActionTarget{Seq: 104, ID: "buy", Window: 4242}
	clicked := true
	cases := []struct {
		name    string
		swipe   bool
		target  *driver.ActionTarget
		answer  map[string]any
		wantErr bool
		want    driver.ActionResult
	}{
		{name: "delivered", target: target,
			answer: map[string]any{"result": "changed", "probed": true, "targeted": true, "seq": 5, "injectMs": 12, "settleMs": 80},
			want:   driver.ActionResult{Settle: driver.Changed, Probed: true, Targeted: true, InjectMs: 12, SettleMs: 80}},
		// Issue #134: how the probe saw the touch end is passed on, for the tap's trace line.
		{name: "the touch's ending is passed on", target: target,
			answer: map[string]any{"result": "changed", "guarded": true, "probed": true, "targeted": true, "touch": "ended", "seq": 5},
			want:   driver.ActionResult{Settle: driver.Changed, Guarded: true, Probed: true, Targeted: true, Touch: "ended"}},
		{name: "no receipt for the touch", target: target,
			answer: map[string]any{"result": "no_change", "guarded": true, "probed": true, "touch": "none", "seq": 5},
			want:   driver.ActionResult{Settle: driver.NoChange, Guarded: true, Probed: true, Touch: "none"}},
		{name: "withheld before the touch", target: target,
			answer: map[string]any{"result": "no_change", "withheld": "preflight", "guarded": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.NoChange, Withheld: "preflight", Guarded: true}},
		{name: "withheld at delivery", target: target,
			answer: map[string]any{"result": "no_change", "withheld": "delivery", "guarded": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.NoChange, Withheld: "delivery", Guarded: true}},
		// Issue #103: withheld at delivery because the target moved after the agent read it.
		{name: "withheld at delivery, the target moved", target: target,
			answer: map[string]any{"result": "no_change", "withheld": "moved", "guarded": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.NoChange, Withheld: "moved", Guarded: true}},
		{name: "withheld but not guarded", target: target,
			answer: map[string]any{"withheld": "preflight", "seq": 5}, wantErr: true},
		{name: "withheld without a target", target: nil,
			answer: map[string]any{"withheld": "preflight", "guarded": true, "seq": 5}, wantErr: true},
		{name: "withheld for an unknown reason", target: target,
			answer: map[string]any{"withheld": "maybe", "guarded": true, "seq": 5}, wantErr: true},
		{name: "withheld yet clicked", target: target,
			answer: map[string]any{"withheld": "delivery", "guarded": true, "clicked": true, "seq": 5}, wantErr: true},
		{name: "a swipe is never withheld", swipe: true, target: target,
			answer: map[string]any{"withheld": "preflight", "guarded": true, "seq": 5}, wantErr: true},
		// Issue #131: the probe saw the touch end cancelled; no handler took it as a tap.
		{name: "cancelled", target: target,
			answer: map[string]any{"cancelled": true, "guarded": true, "probed": true, "seq": 5},
			want:   driver.ActionResult{Cancelled: true, Guarded: true, Probed: true}},
		{name: "cancelled but not guarded", target: target,
			answer: map[string]any{"cancelled": true, "probed": true, "seq": 5}, wantErr: true},
		{name: "cancelled without a target", target: nil,
			answer: map[string]any{"cancelled": true, "guarded": true, "probed": true, "seq": 5}, wantErr: true},
		{name: "cancelled yet clicked", target: target,
			answer: map[string]any{"cancelled": true, "guarded": true, "probed": true, "clicked": true, "seq": 5}, wantErr: true},
		{name: "cancelled and withheld", target: target,
			answer: map[string]any{"cancelled": true, "withheld": "delivery", "guarded": true, "probed": true, "seq": 5}, wantErr: true},
		{name: "a swipe is never cancelled", swipe: true, target: target,
			answer: map[string]any{"cancelled": true, "guarded": true, "probed": true, "seq": 5}, wantErr: true},
		// Issue #149: a solid change after the host's read, before the touch went out.
		{name: "a change since the read is passed on", target: target,
			answer: map[string]any{"result": "changed", "guarded": true, "probed": true, "targeted": true, "changedSinceRead": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.Changed, Guarded: true, Probed: true, Targeted: true, ChangedSinceRead: true}},
		{name: "an older agent says nothing of a change since the read", target: target,
			answer: map[string]any{"result": "changed", "guarded": true, "probed": true, "targeted": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.Changed, Guarded: true, Probed: true, Targeted: true}},
		{name: "a change since the read counts only for a targeted tap", target: nil,
			answer: map[string]any{"result": "changed", "changedSinceRead": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.Changed}},
		{name: "clicked is passed on", target: target,
			answer: map[string]any{"result": "changed", "clicked": true, "seq": 5},
			want:   driver.ActionResult{Settle: driver.Changed, Clicked: &clicked}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newFakeRunner(t)
			answer := func(map[string]any) map[string]any { return c.answer }
			r.on("tap", answer)
			r.on("swipe", answer)
			d := newIOSDriver(t, r)
			o := driver.ActionOpts{Target: c.target}
			var res driver.ActionResult
			var err error
			if c.swipe {
				res, err = d.Swipe(ctxT(t), 10, 500, 10, 100, 0, o)
			} else {
				res, err = d.Tap(ctxT(t), 100, 200, o)
			}
			if c.wantErr {
				if err == nil || !strings.Contains(err.Error(), "invalid tap delivery receipt") {
					t.Errorf("err = %v, result %+v; want an invalid receipt error", err, res)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Seq <= 0 {
				t.Errorf("seq %d, want the runner's change count mapped onto the driver's", res.Seq)
			}
			if (res.Clicked == nil) != (c.want.Clicked == nil) || (res.Clicked != nil && *res.Clicked != *c.want.Clicked) {
				t.Errorf("clicked %v, want %v", res.Clicked, c.want.Clicked)
			}
			res.Seq, res.Clicked, c.want.Clicked = 0, nil, nil
			if res != c.want {
				t.Errorf("result %+v\nwant   %+v", res, c.want)
			}
		})
	}
}

// A targeted tap names its target for the runner's delivery check: the element's id, its app
// process, and the tree generation in the current runner's own count (0 when the tree is from
// a runner before a restart: older than anything this runner has).
func TestTapSendsTheTarget(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 1} })
	d := newIOSDriver(t, r)
	d.mu.Lock()
	d.seqBase = 100 // the runner restarted once: driver 100 is this runner's 0
	d.mu.Unlock()
	for _, c := range []struct {
		seq, want int64
	}{{104, 4}, {100, 0}, {60, 0}} {
		if _, err := d.Tap(ctxT(t), 1, 2, driver.ActionOpts{Target: &driver.ActionTarget{Seq: c.seq, ID: "buy", Window: 77}}); err != nil {
			t.Fatal(err)
		}
		reqs := r.requests("tap")
		q := reqs[len(reqs)-1]
		if q["targetID"] != "buy" || q["targetPID"] != float64(77) || q["targetSeq"] != float64(c.want) {
			t.Errorf("tree %d: request %v; want targetSeq %d", c.seq, q, c.want)
		}
		if _, ok := q["targetMoving"]; ok {
			t.Errorf("tree %d: a still target was sent as moving: %v", c.seq, q)
		}
	}
	// Issue #103: a target that may still be moving is tapped if the point is on it.
	if _, err := d.Tap(ctxT(t), 1, 2, driver.ActionOpts{Target: &driver.ActionTarget{Seq: 104, ID: "buy", Window: 77, Moving: true}}); err != nil {
		t.Fatal(err)
	}
	if reqs := r.requests("tap"); reqs[len(reqs)-1]["targetMoving"] != true {
		t.Errorf("a moving target was not sent as moving: %v", reqs[len(reqs)-1])
	}
	if _, err := d.Tap(ctxT(t), 1, 2, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	reqs := r.requests("tap")
	if q := reqs[len(reqs)-1]; q["targetID"] != nil || q["targetSeq"] != nil {
		t.Errorf("an untargeted tap named a target: %v", q)
	}
}

// ReleaseDevice stops every tb runner serving this simulator in its port range, and leaves
// runners of other simulators alone.
func TestReleaseDeviceStopsOnlyThisSimulatorsRunners(t *testing.T) {
	port := freePorts(t, 3)
	udid := udidFor(t, port)
	exiting := func(r *fakeRunner) {
		r.on("exit", func(map[string]any) map[string]any {
			go r.close()
			return map[string]any{"bye": true}
		})
	}
	own := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, BuildID())
	exiting(own)
	other := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port+1), "OTHER-SIMULATOR", BuildID())
	exiting(other)
	ownToo := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port+2), udid, BuildID())
	exiting(ownToo)

	if err := ReleaseDevice(ctxT(t), udid); err != nil {
		t.Fatal(err)
	}
	if len(own.requests("exit")) != 1 || len(ownToo.requests("exit")) != 1 {
		t.Errorf("exits: own %d, own too %d; want 1 each", len(own.requests("exit")), len(ownToo.requests("exit")))
	}
	if n := len(other.requests("exit")); n != 0 {
		t.Errorf("another simulator's runner was stopped (%d exits)", n)
	}
	for _, p := range []int{port, port + 2} {
		if c, _ := probePort(p); c != nil {
			c.Close()
			t.Errorf("port %d still served", p)
		}
	}
}

// Keep asks a connected runner to stay after the connection closes; with none, it does nothing.
func TestKeep(t *testing.T) {
	if err := (&Driver{}).Keep(ctxT(t)); err != nil {
		t.Errorf("Keep with no connection: %v", err)
	}
	r := newFakeRunner(t)
	r.on("keep", func(map[string]any) map[string]any { return nil })
	d := newIOSDriver(t, r)
	if err := d.Keep(ctxT(t)); err != nil || len(r.requests("keep")) != 1 {
		t.Errorf("Keep: %v, requests %v", err, r.requests("keep"))
	}
	r.on("keep", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "old runner"} })
	if err := d.Keep(ctxT(t)); err == nil {
		t.Error("runner error not returned")
	}
}

// copyTree copies files with their permissions, folders, and symlinks as links (an app
// container's contents, for a fast clearState snapshot).
func TestCopyTree(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "Library", "Caches"), 0o755))
	must(os.WriteFile(filepath.Join(src, "Library", "prefs.plist"), []byte("<plist/>"), 0o644))
	must(os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	must(os.Symlink("Library/prefs.plist", filepath.Join(src, "link")))

	dst := filepath.Join(t.TempDir(), "dst")
	must(copyTree(src, dst))

	if b, err := os.ReadFile(filepath.Join(dst, "Library", "prefs.plist")); err != nil || string(b) != "<plist/>" {
		t.Errorf("file: %q, %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "Library", "Caches")); err != nil || !fi.IsDir() {
		t.Errorf("empty folder not copied: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable bit lost: %v %v", fi.Mode(), err)
	}
	if link, err := os.Readlink(filepath.Join(dst, "link")); err != nil || link != "Library/prefs.plist" {
		t.Errorf("symlink: %q, %v", link, err)
	}

	// Copying over an existing file truncates it; a missing source is an error.
	must(os.WriteFile(filepath.Join(src, "Library", "prefs.plist"), []byte("x"), 0o644))
	must(os.Remove(filepath.Join(dst, "link")))
	must(copyTree(src, dst))
	if b, _ := os.ReadFile(filepath.Join(dst, "Library", "prefs.plist")); string(b) != "x" {
		t.Errorf("overwrite left %q", b)
	}
	if err := copyTree(filepath.Join(t.TempDir(), "missing"), dst); err == nil {
		t.Error("missing source not reported")
	}
}
