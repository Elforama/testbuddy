package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/png"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// DarkModer is implemented by drivers that can read and set the system appearance.
type DarkModer interface {
	SetDarkMode(ctx context.Context, on bool) error
	DarkMode(ctx context.Context) (bool, error)
}

func jsTruthy(s string) bool { return js.Truthy(s) }

func parseMs(s string, def time.Duration) (time.Duration, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	if s == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, failf("invalid timeout %q", s)
	}
	return time.Duration(f) * time.Millisecond, nil
}

// exec runs one command. A non-empty status overrides the default (Passed / Failed).
func (r *Runner) exec(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	d := r.Drv
	switch c.Name {
	case "tapOn", "doubleTapOn", "longPressOn":
		return "", r.tap(ctx, c)

	case "assertVisible":
		sel, err := r.resolveSelector(c.Selector)
		if err != nil {
			return "", err
		}
		_, _, _, err = r.find(ctx, sel, r.adjusted(r.opts.LookupTimeout))
		var nf *NotFound
		if errors.As(err, &nf) {
			nf.Msg = "Assertion is false: " + sel.Describe() + " is visible"
		}
		return "", err

	case "assertNotVisible":
		sel, err := r.resolveSelector(c.Selector)
		if err != nil {
			return "", err
		}
		ok, err := r.absent(ctx, sel, r.adjusted(r.opts.LookupTimeout))
		if err != nil {
			return "", err
		}
		if !ok {
			return "", failf("Assertion is false: %s is not visible", sel.Describe())
		}
		return "", nil

	case "assertTrue":
		ok, err := r.evalCondition(ctx, c.Cond, 0)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", failf("Assertion is false: %s", describeCondition(c.Cond))
		}
		return "", nil

	case "extendedWaitUntil":
		t, err := r.eval(c.Timeout)
		if err != nil {
			return "", err
		}
		timeout, err := parseMs(t, r.opts.LookupTimeout)
		if err != nil {
			return "", err
		}
		ok, err := r.evalCondition(ctx, c.Cond, r.adjusted(timeout))
		if err != nil {
			return "", err
		}
		if !ok {
			return "", failf("Assertion is false: %s", describeCondition(c.Cond))
		}
		return "", nil

	case "inputText":
		text, err := r.eval(c.Text)
		if err != nil {
			return "", err
		}
		return "", r.typeText(ctx, text)

	case "inputRandomText", "inputRandomNumber", "inputRandomEmail", "inputRandomPersonName",
		"inputRandomCityName", "inputRandomCountryName", "inputRandomColorName":
		var text string
		switch c.RandomKind {
		case "Text":
			text = js.RandomText(c.Length)
		case "Number":
			text = js.RandomNumber(c.Length)
		case "Email":
			text = js.RandomEmail()
		case "PersonName":
			text = js.RandomPersonName()
		case "CityName":
			text = js.RandomCity()
		case "CountryName":
			text = js.RandomCountry()
		case "ColorName":
			text = js.RandomColor()
		}
		return "", r.typeText(ctx, text)

	case "eraseText":
		r.invalidate()
		_, err := d.EraseText(ctx, c.Count, driver.ActionOpts{NoChangeMs: 300})
		return "", err

	case "copyTextFrom":
		sel, err := r.resolveSelector(c.Selector)
		if err != nil {
			return "", err
		}
		n, _, _, err := r.find(ctx, sel, r.lookupTimeout(c.Optional))
		if err != nil {
			return "", err
		}
		for _, v := range []string{n.Text, n.Hint, n.Desc, n.Error} {
			if v != "" {
				r.copied = &v
				return "", nil
			}
		}
		return "", failf("Unable to copy text from element: it has no text")

	case "setClipboard":
		text, err := r.eval(c.Text)
		if err != nil {
			return "", err
		}
		r.copied = &text
		return "", nil

	case "pasteText":
		if r.copied == nil {
			return "", nil
		}
		return "", r.typeText(ctx, *r.copied)

	case "swipe":
		return "", r.swipe(ctx, c)

	case "scroll":
		w, h := d.Size()
		dur := 400 * time.Millisecond
		if d.Platform() == "ios" {
			dur = 333 * time.Millisecond
		}
		r.invalidate()
		_, err := d.Swipe(ctx, w/2, h/2, w/2, h/10, dur, driver.ActionOpts{})
		return "", err

	case "scrollUntilVisible":
		return "", r.scrollUntilVisible(ctx, c)

	case "back":
		r.invalidate()
		if d.Platform() == "ios" {
			return "", nil // back is a no-op on iOS (the flow format says so)
		}
		return "", r.reactTo(d.PressKey(ctx, "back", driver.ActionOpts{React: true}))

	case "hideKeyboard":
		r.invalidate()
		if err := d.HideKeyboard(ctx); err != nil {
			return "", err
		}
		s, err := r.read(ctx)
		if err != nil {
			return "", err
		}
		if s.Keyboard {
			return "", failf("Couldn't hide the keyboard. Some apps ignore the hide request; tap outside the field instead")
		}
		return "", nil

	case "pressKey":
		r.invalidate()
		err := r.reactTo(d.PressKey(ctx, c.Text, driver.ActionOpts{React: true}))
		if err != nil && d.Platform() == "ios" {
			return "", nil // keys iOS lacks silently do nothing there (the flow format says so)
		}
		return "", err

	case "launchApp":
		appID, err := r.appIDFor(c)
		if err != nil {
			return "", err
		}
		perms, err := r.permissions(c.Permissions)
		if err != nil {
			return "", err
		}
		if perms == nil {
			perms = map[string]string{"all": "allow"}
		}
		r.notifications = strings.ToLower(perms["all"])
		if v, ok := perms["notifications"]; ok {
			r.notifications = strings.ToLower(v)
		}
		args := map[string]any{}
		for _, a := range c.Arguments {
			k, err := r.eval(a.Key)
			if err != nil {
				return "", err
			}
			v := a.Value
			if s, ok := v.(string); ok {
				if v, err = r.eval(s); err != nil {
					return "", err
				}
			}
			if _, dup := args[k]; dup {
				return "", failf("duplicate launch argument %q", k)
			}
			args[k] = v
		}
		stop := true
		if c.StopApp != nil {
			stop = *c.StopApp
		}
		r.invalidate()
		if c.ClearKeychain {
			if err := d.ClearKeychain(ctx); err != nil {
				return "", err
			}
		}
		err = d.LaunchApp(ctx, appID, driver.LaunchOpts{ClearState: c.ClearState, StopApp: stop, Permissions: perms, Arguments: args})
		if err != nil {
			return "", failf("Unable to launch app %s: %v", appID, err)
		}
		return "", nil

	case "stopApp", "killApp", "clearState":
		appID, err := r.appIDFor(c)
		if err != nil {
			return "", err
		}
		r.invalidate()
		switch c.Name {
		case "stopApp":
			return "", d.StopApp(ctx, appID)
		case "killApp":
			return "", d.KillApp(ctx, appID)
		default:
			if err := d.ClearState(ctx, appID); err != nil {
				return "", err
			}
			return "", d.SetPermissions(ctx, appID, map[string]string{"all": "unset"})
		}

	case "clearKeychain":
		return "", d.ClearKeychain(ctx)

	case "setPermissions":
		appID, err := r.appIDFor(c)
		if err != nil {
			return "", err
		}
		perms, err := r.permissions(c.Permissions)
		if err != nil {
			return "", err
		}
		return "", d.SetPermissions(ctx, appID, perms)

	case "openLink":
		link, err := r.eval(c.Text)
		if err != nil {
			return "", err
		}
		r.invalidate()
		if err := d.OpenLink(ctx, link, r.appID, c.AutoVerify, c.Browser); err != nil {
			return "", err
		}
		d.WaitSettled(ctx, 3*time.Second)
		return "", nil

	case "runFlow":
		return r.runFlowCmd(ctx, c, f)

	case "runScript":
		ok, err := r.evalCondition(ctx, c.When, r.adjusted(r.opts.OptionalTimeout))
		if err != nil {
			return "", err
		}
		if !ok {
			return Skipped, nil
		}
		r.pushEnv()
		defer r.popEnv()
		if err := r.define(c.Env); err != nil {
			return "", err
		}
		return "", r.jsEngine().RunScript(c.Script, dirOf(c.SubFile), r.scope())

	case "evalScript":
		_, err := r.eval(c.Text)
		return "", err

	case "repeat":
		return r.repeat(ctx, c, f)

	case "retry":
		return r.retry(ctx, c, f)

	case "takeScreenshot":
		path, err := r.eval(c.Text)
		if err != nil {
			return "", err
		}
		out, err := r.artifactPath("takeScreenshot", path, ".png")
		if err != nil {
			return "", err
		}
		img, err := r.screenshot(ctx, c.CropOn)
		if err != nil {
			return "", err
		}
		return "", os.WriteFile(out, img, 0o644)

	case "assertScreenshot":
		return "", r.assertScreenshot(ctx, c)

	case "startRecording":
		path, err := r.eval(c.Text)
		if err != nil {
			return "", err
		}
		out, err := r.artifactPath("startRecording", path, ".mp4")
		if err != nil {
			return "", err
		}
		if err := d.StartRecording(ctx, out); err != nil {
			return "", err
		}
		r.recording = true
		return "", nil

	case "stopRecording":
		if !r.recording {
			return "", nil
		}
		r.recording = false
		return "", d.StopRecording(ctx)

	case "waitForAnimationToEnd":
		t, err := r.eval(c.Timeout)
		if err != nil {
			return "", err
		}
		timeout, err := parseMs(t, 15*time.Second)
		if err != nil || timeout <= 0 {
			timeout = 15 * time.Second
		}
		d.WaitSettled(ctx, timeout) // never fails (the flow format says so)
		return "", nil

	case "setLocation":
		lat, lon, err := r.latLon(c.Latitude, c.Longitude)
		if err != nil {
			return "", err
		}
		return "", d.SetLocation(ctx, lat, lon)

	case "travel":
		return "", r.travel(ctx, c)

	case "setOrientation":
		o, err := r.eval(c.Text)
		if err != nil {
			return "", err
		}
		norm := flow.NormalizeOrientation(o)
		if norm == "" {
			return Failed, fmt.Errorf("invalid orientation %q", o)
		}
		r.invalidate()
		if err := d.SetOrientation(ctx, norm); err != nil {
			return "", err
		}
		d.WaitSettled(ctx, 3*time.Second)
		return "", nil

	case "addMedia":
		return "", d.AddMedia(ctx, c.Files)

	case "setAirplaneMode":
		return "", d.SetAirplaneMode(ctx, c.Enabled)

	case "toggleAirplaneMode":
		return "", d.ToggleAirplaneMode(ctx)

	case "setDarkMode", "toggleDarkMode", "assertDarkMode", "assertLightMode":
		dm, ok := d.(DarkModer)
		if !ok {
			return "", failf("%s is not supported on %s", c.Name, d.Platform())
		}
		switch c.Name {
		case "setDarkMode":
			return "", dm.SetDarkMode(ctx, c.Enabled)
		case "toggleDarkMode":
			on, err := dm.DarkMode(ctx)
			if err != nil {
				return "", err
			}
			return "", dm.SetDarkMode(ctx, !on)
		default:
			on, err := dm.DarkMode(ctx)
			if err != nil {
				return "", err
			}
			if on != (c.Name == "assertDarkMode") {
				return "", failf("Assertion is false: dark mode is %s", onOff(on))
			}
			return "", nil
		}

	case "assertWithAI", "assertNoDefectsWithAI", "extractTextWithAI":
		return "", failf("%s is not supported by Test Buddy (it needs a cloud AI service)", c.Name)

	case "signal":
		if r.opts.Signals == nil {
			return "", nil
		}
		r.opts.Signals.Send(c.Signal)
		return "", nil

	case "waitForSignal":
		if r.opts.Signals == nil {
			return "", failf("waitForSignal %q: this flow does not run on several devices", c.Signal)
		}
		t, err := r.eval(c.Timeout)
		if err != nil {
			return "", err
		}
		timeout, err := parseMs(t, 120*time.Second)
		if err != nil {
			return "", err
		}
		return "", r.opts.Signals.Wait(ctx, c.Signal, timeout)

	case "on", "parallel":
		return "", failf("%s blocks run only in multi-device flows", c.Name)
	}
	return "", fmt.Errorf("command %s is not implemented", c.Name)
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func (r *Runner) appIDFor(c *flow.Command) (string, error) {
	id := c.AppID
	if id == "" {
		id = r.appID
	}
	v, err := r.eval(id)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", failf("no appId: set appId in the flow config or on the command")
	}
	return v, nil
}

func (r *Runner) permissions(kvs []flow.KV) (map[string]string, error) {
	if kvs == nil {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range kvs {
		v, err := r.eval(kv.Value)
		if err != nil {
			return nil, err
		}
		out[kv.Key] = v
	}
	return out, nil
}

func (r *Runner) typeText(ctx context.Context, text string) error {
	r.invalidate()
	_, err := r.Drv.TypeText(ctx, text, driver.ActionOpts{NoChangeMs: 300})
	return err
}

// ---- tap ----

func (r *Runner) point(spec string, within ui.Rect, elementRelative bool) (int, int, error) {
	a, b, ok := strings.Cut(spec, ",")
	if !ok {
		return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q", spec)}
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.HasSuffix(a, "%") || strings.HasSuffix(b, "%") {
		px, e1 := strconv.Atoi(strings.TrimSuffix(a, "%"))
		py, e2 := strconv.Atoi(strings.TrimSuffix(b, "%"))
		if e1 != nil || e2 != nil || px < 0 || px > 100 || py < 0 || py > 100 {
			return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q: percentages are 0-100", spec)}
		}
		return within.L + within.W()*px/100, within.T + within.H()*py/100, nil
	}
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if e1 != nil || e2 != nil || x < 0 || y < 0 {
		return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q", spec)}
	}
	if elementRelative {
		if x >= within.W() || y >= within.H() {
			return 0, 0, &invalidField{msg: fmt.Sprintf("point %q is outside the element", spec)}
		}
		return within.L + x, within.T + y, nil
	}
	return x, y, nil
}

func (r *Runner) tap(ctx context.Context, c *flow.Command) error {
	d := r.Drv
	w, h := d.Size()
	var x, y int
	var tapped *ui.Node // the element tapped (nil: a point on the screen)
	settle := driver.ActionOpts{}
	if c.WaitToSettleTimeoutMs > 0 {
		settle.Timeout = time.Duration(c.WaitToSettleTimeoutMs) * time.Millisecond
	}
	if c.Selector == nil { // a point on the screen
		p, err := r.eval(c.Point)
		if err != nil {
			return err
		}
		if x, y, err = r.point(p, ui.Rect{R: w, B: h}, false); err != nil {
			return err
		}
	} else {
		sel, err := r.resolveSelector(c.Selector)
		if err != nil {
			return err
		}
		n, s, err := r.findStable(ctx, sel, r.lookupTimeout(c.Optional), settle.Timeout)
		if err != nil {
			return err
		}
		if !n.Enabled && sel.Enabled == nil {
			n, s = r.awaitEnabled(ctx, sel, n, s, disabledWait)
		}
		tapped = n
		if c.Point != "" {
			p, err := r.eval(c.Point)
			if err != nil {
				return err
			}
			if x, y, err = r.point(p, n.Bounds, true); err != nil {
				return err
			}
		} else if x, y, err = r.clearAndPoint(ctx, sel, s, n); err != nil {
			return err
		}
	}
	attempts := 1
	if c.RetryTapIfNoChange {
		attempts = 2
	}
	// The tap waits for the UI to start reacting (so the next command cannot act on the screen
	// it left), not for it to finish: see Runner.unsettled.
	settleFor := settle.Timeout
	if settleFor == 0 {
		settleFor = 5 * time.Second
	}
	if !c.LongPress {
		settle.React = true
	}
	if d.Platform() == "ios" {
		settle.Hold = iosTapHold(tapped, c.Point != "" || c.Selector == nil)
	}
	for i := 0; i < attempts; i++ {
		r.invalidate()
		var res driver.ActionResult
		var err error
		switch {
		case c.LongPress:
			res, err = d.LongPress(ctx, x, y, 3*time.Second, settle)
		case c.Repeat > 1 && c.Name == "doubleTapOn":
			res, err = d.DoubleTap(ctx, x, y, settle)
		case c.Repeat > 1:
			for k := 0; k < c.Repeat && err == nil; k++ {
				o := settle
				if k < c.Repeat-1 {
					o.NoSettle = true
				}
				res, err = d.Tap(ctx, x, y, o)
				if k < c.Repeat-1 && c.Delay > 0 {
					time.Sleep(time.Duration(c.Delay) * time.Millisecond) // the flow asked for this gap
				}
			}
		default:
			res, err = d.Tap(ctx, x, y, settle)
		}
		if err != nil {
			return err
		}
		if res.Settle != driver.NoChange {
			if settle.React {
				r.unsettled = settleFor
			}
			break
		}
	}
	return nil
}

// iOS tap holds. Most taps are held 10 ms: React Native cancels a press when a scroll view
// near it (a carousel paging itself) moves during the touch, which a longer hold makes likelier
// (measured: 1 lost tap in 30 at 20 ms, none in 576 at 10 ms). UIKit's switches and
// other tracking controls need the touch to span a display frame (a UISwitch toggles reliably
// from 17 ms), and system alerts' and sheets' buttons are UIKit: 20 ms for those, and for taps
// at a point, which may land on anything.
const iosShortHold, iosFrameHold = 10 * time.Millisecond, 20 * time.Millisecond

var iosFrameHoldTypes = map[string]bool{
	"Switch": true, "Toggle": true, "CheckBox": true, "RadioButton": true, "Slider": true,
	"Stepper": true, "SegmentedControl": true, "PickerWheel": true, "Picker": true, "DatePicker": true,
	"PopUpButton": true, "ComboBox": true, "MenuButton": true,
}

func iosTapHold(n *ui.Node, atPoint bool) time.Duration {
	if n == nil || atPoint || iosFrameHoldTypes[n.Class] {
		return iosFrameHold
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Class == "Alert" || p.Class == "Sheet" || iosFrameHoldTypes[p.Class] {
			return iosFrameHold
		}
	}
	return iosShortHold
}

// disabledWait: how long a tap gives a disabled target to turn on.
const disabledWait = 3 * time.Second

// awaitEnabled gives a disabled element up to timeout to turn on, on change events: a button
// that waits for its form to validate takes a tap and does nothing, and the flow would fail
// later for no visible reason. A selector that names `enabled` itself is never waited for.
// It returns the element as found last (still disabled after the timeout, it is tapped
// anyway, as the flow format does).
func (r *Runner) awaitEnabled(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, timeout time.Duration) (*ui.Node, *ui.Screen) {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	seq := s.Seq
	for !n.Enabled && time.Now().Before(deadline) {
		r.waitAny(ctx, events, seq, deadline)
		if ctx.Err() != nil {
			break
		}
		seq = r.Drv.Seq()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			continue
		}
		n, s = n2, s2
	}
	if n.Enabled {
		r.Drv.WaitSettled(ctx, 2*time.Second)
		if n2, s2, _, err := r.find(ctx, sel, 0); err == nil {
			n, s = n2, s2
		}
	}
	return n, s
}

// reactTo notes that an action run with ActionOpts.React changed the UI and returned before
// it settled: the next command that needs a still screen waits for it (Runner.unsettled).
func (r *Runner) reactTo(res driver.ActionResult, err error) error {
	if err == nil && res.Settle == driver.Changed {
		r.unsettled = 5 * time.Second
	}
	return err
}

// ---- swipe & scroll ----

func (r *Runner) swipe(ctx context.Context, c *flow.Command) error {
	d := r.Drv
	w, h := d.Size()
	screen := ui.Rect{R: w, B: h}
	dur := time.Duration(c.Duration) * time.Millisecond
	opts := driver.ActionOpts{}
	if c.WaitToSettleTimeoutMs > 0 {
		opts.Timeout = time.Duration(c.WaitToSettleTimeoutMs) * time.Millisecond
	}
	var x1, y1, x2, y2 int
	switch {
	case c.From != nil:
		sel, err := r.resolveSelector(c.From)
		if err != nil {
			return err
		}
		n, _, err := r.findStable(ctx, sel, r.lookupTimeout(c.Optional), 0)
		if err != nil {
			return err
		}
		x1, y1 = n.Bounds.CX(), n.Bounds.CY()
		if c.Point != "" {
			p, err := r.eval(c.Point)
			if err != nil {
				return err
			}
			if x1, y1, err = r.point(p, n.Bounds, true); err != nil {
				return err
			}
		}
		x2, y2 = x1, y1
		switch c.Direction {
		case "UP":
			y2 = h / 10
		case "DOWN":
			y2 = h * 9 / 10
		case "LEFT":
			x2 = w / 10
		case "RIGHT":
			x2 = w * 9 / 10
		}
	case c.Direction != "":
		fx1, fy1, fx2, fy2 := directionSwipe(c.Direction, d.Platform())
		x1, y1, x2, y2 = int(fx1*float64(w)), int(fy1*float64(h)), int(fx2*float64(w)), int(fy2*float64(h))
	default:
		start, err := r.eval(c.Start)
		if err != nil {
			return err
		}
		end, err := r.eval(c.End)
		if err != nil {
			return err
		}
		if x1, y1, err = r.point(start, screen, false); err != nil {
			return err
		}
		if x2, y2, err = r.point(end, screen, false); err != nil {
			return err
		}
	}
	if d.Platform() == "ios" {
		clamp := func(v, hi int) int { return max(0, min(hi-1, v)) }
		x1, y1, x2, y2 = clamp(x1, w), clamp(y1, h), clamp(x2, w), clamp(y2, h)
	}
	r.invalidate()
	_, err := d.Swipe(ctx, x1, y1, x2, y2, dur, opts)
	return err
}

func directionSwipe(dir, platform string) (x1, y1, x2, y2 float64) {
	switch dir {
	case "UP":
		if platform == "ios" {
			return 0.5, 0.9, 0.5, 0.1
		}
		return 0.5, 0.5, 0.5, 0.1
	case "DOWN":
		return 0.5, 0.2, 0.5, 0.9
	case "RIGHT":
		return 0.1, 0.5, 0.9, 0.5
	default: // LEFT
		return 0.9, 0.5, 0.1, 0.5
	}
}

func (r *Runner) scrollUntilVisible(ctx context.Context, c *flow.Command) error {
	d := r.Drv
	sel, err := r.resolveSelector(c.Selector)
	if err != nil {
		return err
	}
	t, err := r.eval(c.Timeout)
	if err != nil {
		return err
	}
	timeout, err := parseMs(t, 20*time.Second)
	if err != nil {
		return err
	}
	if timeout < 0 {
		timeout = 20 * time.Second
	}
	speed := 40
	if c.Speed != "" {
		sp, err := r.eval(c.Speed)
		if err != nil {
			return err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(sp), 64)
		if err != nil || f < 0 || f > 100 {
			return &invalidField{msg: fmt.Sprintf("speed must be 0-100, got %q", sp)}
		}
		speed = int(f)
	}
	dur := time.Duration(1000*(100-speed)/100+1) * time.Millisecond
	need := 0.0
	if c.VisibilityPct >= 100 {
		need = 1.0
	}
	w, h := d.Size()
	screen := ui.Rect{R: w, B: h}
	vertical := c.Direction == "DOWN" || c.Direction == "UP"
	// Drag opposite to the way the content should travel: up and down from the center to near
	// the edge, sideways across 70% of the width (past half, so a paging list turns its page).
	x1, y1 := w/2, h/2
	var x2, y2 int
	switch c.Direction {
	case "DOWN":
		x2, y2 = w/2, h/10
	case "UP":
		x2, y2 = w/2, h*9/10
	case "RIGHT":
		x1, x2, y2 = w*85/100, w*15/100, h/2
	default: // LEFT
		x1, x2, y2 = w*15/100, w*85/100, h/2
	}
	fx1, fy1 := w/2, h/2 // a flinging swipe from the center: the fallback below
	// `speed` sets a swipe's duration. These drags do not fling, so it changes little: how far
	// the content moves is the drag's length.

	// Each swipe is a drag that does not fling (ActionOpts.Drag): the content stops as the finger
	// lifts. A flinging swipe moves it farther, but it coasts on for 0.5-2 s that would have to
	// be waited out before each look. So the screen is read right after each drag, dragged
	// again at once while the element is not there, and only checked for rest once it shows.
	drag := driver.ActionOpts{NoSettle: true, Drag: true}
	events, stop := d.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	centerTries := 0
	placed := false       // the drag's start moved into the list (iOS, up and down)
	var dragged time.Time // when the last drag began; zero once the screen is known to be still
	var before uint64     // what the list under the drag looked like when it began
	var stalled time.Time // since when drags have moved nothing
	var last ui.Rect      // where the element was in the previous read
	// Drags that brought nothing new into view. A paging list springs back from a drag that
	// does not fling (when it is shorter than a page), so after two the swipes fling, as
	// the classic swipes do.
	seen := map[string]bool{}
	fruitless, fling := 0, false
	for {
		s, err := r.read(ctx)
		if err != nil {
			return err
		}
		n, _, err := r.match(ctx, s, sel)
		if err != nil {
			return &invalidField{msg: err.Error()}
		}
		vis := 0.0
		if n != nil {
			vis = match.VisibleShare(n.Bounds, screen)
		}
		centering := c.CenterElement && vis > 0.1 && centerTries <= 4
		done := n != nil && !centering && vis >= need
		if centering && centered(n.Bounds, screen, c.Direction) {
			done, centering = true, false
		}
		unmoved := !dragged.IsZero() && listSignature(s, x1, y1, vertical) == before
		if unmoved && time.Since(dragged) < 500*time.Millisecond && time.Now().Before(deadline) {
			// The drag has not shown yet (the app is a frame or two behind): another one now
			// could carry the element past the screen.
			r.waitAny(ctx, events, s.Seq, dragged.Add(500*time.Millisecond))
			continue
		}
		if !dragged.IsZero() && n != nil && (done || centering || vis > 0) {
			// It shows after a drag. The content stops at the lift, but an app can take a frame
			// or two to move it, a list end bounces back, a paging list snaps: it counts once two
			// reads in a row agree on where it is (or the UI settles, after a second).
			if n.Bounds != last {
				last = n.Bounds
				if time.Since(dragged) < time.Second {
					r.waitAny(ctx, events, s.Seq, time.Now().Add(80*time.Millisecond))
					continue
				}
				d.WaitSettled(ctx, 2*time.Second)
			}
			dragged = time.Time{}
			continue
		}
		last = ui.Rect{}
		if n != nil {
			last = n.Bounds
		}
		if done {
			return nil
		}
		if unmoved && fruitless == 0 {
			// Not there, and the drag moved nothing: a list still loading, or at its end. The
			// next drag waits up to 2 s for the screen to change (once: after that, the
			// swipes fling, below).
			if stalled.IsZero() {
				stalled = dragged
			}
			if dl := stalled.Add(2 * time.Second); time.Now().Before(dl) && time.Now().Before(deadline) {
				r.waitAny(ctx, events, s.Seq, dl)
				continue
			}
		}
		stalled = time.Time{}
		if !time.Now().Before(deadline) {
			return failf("No visible element found: %s", sel.Describe())
		}
		if !fling && !dragged.IsZero() && n == nil {
			if newTexts(s, x1, y1, seen) == 0 {
				fruitless++
			} else {
				fruitless = 0
			}
			fling = fruitless >= 2
		} else if dragged.IsZero() {
			newTexts(s, x1, y1, seen)
		}
		if fling {
			// A classic swipe: it flings, and settles before the next look.
			r.invalidate()
			if _, err := d.Swipe(ctx, fx1, fy1, x2, y2, dur, driver.ActionOpts{NoChangeMs: 300}); err != nil {
				return err
			}
			dragged, last = time.Time{}, ui.Rect{}
			continue
		}
		if vertical && !placed {
			y1, placed = dragStart(s, d.Platform(), x1, y1, c.Direction)
		}
		tx, ty := x2, y2
		if centering {
			// Just far enough to bring it to the middle, at most a whole drag.
			centerTries++
			if vertical {
				stride := abs(y2 - y1)
				ty = y1 + clamp(h/2-n.Bounds.CY(), -stride, stride)
			} else {
				stride := abs(x2 - x1)
				tx = x1 + clamp(w/2-n.Bounds.CX(), -stride, stride)
			}
		}
		before = listSignature(s, x1, y1, vertical)
		r.invalidate()
		dragged = time.Now()
		if _, err := d.Swipe(ctx, x1, y1, tx, ty, dur, drag); err != nil {
			return err
		}
	}
}

// waitAny waits until the device reports any UI change after seq, or the deadline.
func (r *Runner) waitAny(ctx context.Context, events <-chan driver.Event, seq int64, deadline time.Time) {
	if r.Drv.Seq() > seq {
		return
	}
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			return
		case e, ok := <-events:
			if !ok || e.Kind == "disconnected" || e.Seq > seq {
				return
			}
		}
	}
}

// scrollContainer is the innermost scrolling node under (x, y), or nil.
func scrollContainer(s *ui.Screen, x, y int) *ui.Node {
	var best *ui.Node
	for _, n := range s.Nodes {
		if n.Scrollable && n.Bounds.Contains(x, y) && (best == nil || n.IsDescendantOf(best)) {
			best = n
		}
	}
	return best
}

// newTexts adds the texts of the list under (x, y) (else the screen) to seen, and says how many
// it had not seen.
func newTexts(s *ui.Screen, x, y int, seen map[string]bool) int {
	n := 0
	add := func(nd *ui.Node) bool {
		if t := nd.Label(); t != "" && !seen[t] {
			seen[t] = true
			n++
		}
		return true
	}
	if box := scrollContainer(s, x, y); box != nil {
		box.Walk(add)
	} else {
		for _, nd := range s.Nodes {
			add(nd)
		}
	}
	return n
}

// listSignature hashes what a drag through (x, y) moves: the texts and their positions along
// the drag's axis of the list there (else the whole screen). A ticker or shimmer animating
// across the axis leaves it as it was; content that scrolled or arrived changes it.
func listSignature(s *ui.Screen, x, y int, vertical bool) uint64 {
	h := fnv.New64a()
	add := func(n *ui.Node) bool {
		pos := n.Bounds.L
		if vertical {
			pos = n.Bounds.T
		}
		fmt.Fprintf(h, "%d|%s|%s\x00", pos, n.Text, n.Desc)
		return true
	}
	if box := scrollContainer(s, x, y); box != nil {
		box.Walk(add)
	} else {
		for _, n := range s.Nodes {
			add(n)
		}
	}
	return h.Sum64()
}

// dragStart moves an up or down drag's start 70% into the list under (x, y) on iOS, from the
// edge the content heads to: each drag then moves the content 1.4x as far as one from the
// screen's center, still inside the list and clear of a bar along its far edge. iOS drags play
// out in real time (Android's go in at once), so fewer, longer ones pay. placed is false when
// there is no list there yet (a screen still loading).
func dragStart(s *ui.Screen, platform string, x, y int, dir string) (start int, placed bool) {
	if platform != "ios" {
		return y, true
	}
	box := scrollContainer(s, x, y)
	if box == nil {
		return y, false
	}
	top, bottom := max(box.Bounds.T, 0), min(box.Bounds.B, s.Height)
	if dir == "DOWN" {
		start = top + (bottom-top)*7/10
		return max(start, y), true
	}
	start = top + (bottom-top)*3/10
	return min(start, y), true
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func centered(b, screen ui.Rect, dir string) bool {
	if dir == "LEFT" || dir == "RIGHT" {
		mid := screen.W() / 2
		return abs(b.CX()-mid) <= screen.W()/5
	}
	mid := screen.H() / 2
	return abs(b.CY()-mid) <= screen.H()/5
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---- composite commands ----

func (r *Runner) runFlowCmd(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	ok, err := r.evalCondition(ctx, c.When, r.adjusted(r.opts.OptionalTimeout))
	if err != nil {
		return "", err
	}
	if !ok {
		return Skipped, nil
	}
	r.pushEnv()
	defer r.popEnv()
	if err := r.define(c.Env); err != nil {
		return "", err
	}
	if c.SubFlow != nil {
		sub := c.SubFlow
		if err := r.define(sub.Config.Env); err != nil {
			return "", err
		}
		var err error
		if len(sub.Config.OnFlowStart) > 0 {
			err = r.runList(ctx, sub.Config.OnFlowStart, sub, nil)
		}
		if err == nil {
			err = r.runList(ctx, sub.Commands, sub, nil)
		}
		if len(sub.Config.OnFlowComplete) > 0 {
			if cerr := r.runList(ctx, sub.Config.OnFlowComplete, sub, nil); err == nil {
				err = cerr
			}
		}
		return "", err
	}
	return "", r.runList(ctx, c.Commands, f, nil)
}

func (r *Runner) repeat(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	limit := math.MaxInt
	if c.Times != "" {
		t, err := r.eval(c.Times)
		if err != nil {
			return "", err
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			limit = int(v)
		}
	}
	n := 0
	for n < limit {
		if c.While != nil {
			ok, err := r.evalCondition(ctx, c.While, r.adjusted(r.opts.OptionalTimeout))
			if err != nil {
				return "", err
			}
			if !ok {
				break
			}
		}
		r.pushEnv()
		err := r.runList(ctx, c.Commands, f, nil)
		r.popEnv()
		if err != nil {
			return "", err
		}
		n++
	}
	if n == 0 {
		return Skipped, nil
	}
	return "", nil
}

func (r *Runner) retry(ctx context.Context, c *flow.Command, f *flow.Flow) (Status, error) {
	max := 1
	if c.MaxRetries != "" {
		t, err := r.eval(c.MaxRetries)
		if err != nil {
			return "", err
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			max = int(v)
		}
	}
	max = min(max, 3)
	var last error
	for attempt := 0; attempt <= max; attempt++ {
		r.pushEnv()
		err := r.define(c.Env)
		if err == nil {
			if c.SubFlow != nil {
				sub := c.SubFlow
				err = r.define(sub.Config.Env)
				if err == nil && len(sub.Config.OnFlowStart) > 0 {
					err = r.runList(ctx, sub.Config.OnFlowStart, sub, nil)
				}
				if err == nil {
					err = r.runList(ctx, sub.Commands, sub, nil)
				}
				if len(sub.Config.OnFlowComplete) > 0 {
					if cerr := r.runList(ctx, sub.Config.OnFlowComplete, sub, nil); err == nil {
						err = cerr
					}
				}
			} else {
				err = r.runList(ctx, c.Commands, f, nil)
			}
		}
		r.popEnv()
		if err == nil {
			return "", nil
		}
		if !isFlowError(err) {
			return "", err
		}
		last = err
	}
	return "", last
}

// ---- screenshots ----

func (r *Runner) screenshot(ctx context.Context, crop *flow.Selector) ([]byte, error) {
	img, err := r.Drv.Screenshot(ctx, 1, "png")
	if err != nil {
		return nil, err
	}
	if crop == nil {
		return img, nil
	}
	sel, err := r.resolveSelector(crop)
	if err != nil {
		return nil, err
	}
	n, _, _, err := r.find(ctx, sel, r.lookupTimeout(false))
	if err != nil {
		return nil, err
	}
	if n.Bounds.W() <= 0 || n.Bounds.H() <= 0 {
		return nil, failf("cropOn element has no size")
	}
	src, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, err
	}
	w, _ := r.Drv.Size()
	scale := float64(src.Bounds().Dx()) / float64(w)
	rect := image.Rect(int(float64(n.Bounds.L)*scale), int(float64(n.Bounds.T)*scale), int(float64(n.Bounds.R)*scale), int(float64(n.Bounds.B)*scale))
	sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img, nil
	}
	var buf bytes.Buffer
	png.Encode(&buf, sub.SubImage(rect.Intersect(src.Bounds())))
	return buf.Bytes(), nil
}

func (r *Runner) assertScreenshot(ctx context.Context, c *flow.Command) error {
	path, err := r.eval(c.Text)
	if err != nil {
		return err
	}
	lower := strings.ToLower(path)
	if !strings.HasSuffix(lower, ".png") && !strings.HasSuffix(lower, ".jpg") && !strings.HasSuffix(lower, ".jpeg") {
		path += ".png"
	}
	ref := path
	if !strings.HasPrefix(ref, "/") {
		ref = dirOf(c.File) + "/" + path
		if _, err := os.Stat(ref); err != nil && r.opts.ArtifactsDir != "" {
			ref = r.opts.ArtifactsDir + "/takeScreenshot/" + path
		}
	}
	refData, err := os.ReadFile(ref)
	if err != nil {
		return failf("reference screenshot not found: %s", ref)
	}
	thr, err := r.eval(c.Threshold)
	if err != nil {
		return err
	}
	threshold, err := strconv.ParseFloat(thr, 64)
	if err != nil {
		return failf("invalid thresholdPercentage %q", thr)
	}
	actual, err := r.screenshot(ctx, c.CropOn)
	if err != nil {
		return err
	}
	a, _, err1 := image.Decode(bytes.NewReader(refData))
	b, _, err2 := image.Decode(bytes.NewReader(actual))
	if err1 != nil || err2 != nil {
		return failf("could not decode screenshots")
	}
	if a.Bounds().Size() != b.Bounds().Size() {
		return failf("screenshot size %v differs from the reference %v", b.Bounds().Size(), a.Bounds().Size())
	}
	same, total := 0, 0
	ab, bb := a.Bounds(), b.Bounds()
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			r1, g1, b1, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			dr, dg, db := float64(r1)-float64(r2), float64(g1)-float64(g2), float64(b1)-float64(b2)
			dist := math.Sqrt(dr*dr+dg*dg+db*db) / (65535 * math.Sqrt(3))
			if dist <= 0.1 {
				same++
			}
			total++
		}
	}
	pct := 100 * float64(same) / float64(max(1, total))
	if pct < threshold {
		return failf("screenshot matches %.1f%% of the reference, below the threshold %.1f%%", pct, threshold)
	}
	return nil
}

// ---- location ----

func (r *Runner) latLon(a, b string) (float64, float64, error) {
	sa, err := r.eval(a)
	if err != nil {
		return 0, 0, err
	}
	sb, err := r.eval(b)
	if err != nil {
		return 0, 0, err
	}
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(sa), 64)
	lon, e2 := strconv.ParseFloat(strings.TrimSpace(sb), 64)
	if e1 != nil || e2 != nil {
		return 0, 0, failf("invalid location %q, %q", sa, sb)
	}
	return lat, lon, nil
}

func (r *Runner) travel(ctx context.Context, c *flow.Command) error {
	if len(c.Points) == 0 {
		return nil
	}
	if err := r.Drv.SetLocation(ctx, c.Points[0][0], c.Points[0][1]); err != nil {
		return err
	}
	for i := 1; i < len(c.Points); i++ {
		a, b := c.Points[i-1], c.Points[i]
		dist := haversine(a[0], a[1], b[0], b[1])
		step := time.Duration(dist / c.TravelSpeed / 50 * float64(time.Second))
		for k := 1; k <= 50; k++ {
			f := float64(k) / 50
			if err := r.Drv.SetLocation(ctx, a[0]+(b[0]-a[0])*f, a[1]+(b[1]-a[1])*f); err != nil {
				return err
			}
			select { // simulated travel time is the command's purpose, not a wait on the app
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(step):
			}
		}
	}
	return nil
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * R * math.Asin(math.Sqrt(h))
}
