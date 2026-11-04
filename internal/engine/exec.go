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
	"path/filepath"
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
		res, err := d.EraseText(ctx, c.Count, driver.ActionOpts{NoChangeMs: 300})
		if err == nil {
			r.tracef("erased up to %d characters: %s", c.Count, actionDesc(res))
		}
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
		if stop || c.ClearState {
			if err := r.closeStaleAlerts(ctx); err != nil {
				return "", err
			}
		}
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
		if d.Platform() == "web" {
			// Not supported on the web yet: a recording is a debugging aid, not part of what
			// a flow checks, so the flow goes on without one (it failed here before).
			r.logLine("screen recording is not supported on web yet: " + filepath.Base(out) + " is not recorded")
			return "", nil
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
	// The focused field from the last read, when it is still current (no extra read: typing
	// should not wait for one).
	if s := r.LastScreen(); s != nil && s.Seq == r.Drv.Seq() {
		if f := focusedField(s); f != nil {
			r.tracef("typing %d characters into %s", len([]rune(text)), nodeDesc(f))
		} else {
			r.tracef("typing %d characters, but no field has focus (tap the field first)", len([]rune(text)))
		}
	} else {
		r.tracef("typing %d characters", len([]rune(text)))
	}
	r.invalidate()
	if r.Drv.Platform() == "android" && len([]rune(text)) > 1 {
		// The first key alone, checked: a field that has just taken focus (a screen that
		// opens with its keyboard up) can drop a key that arrives before the keyboard is
		// connected to it. The rest follows once the field shows the first.
		rest, err := r.typeFirstKey(ctx, text)
		if err != nil || rest == "" {
			return err
		}
		text = rest
	}
	res, err := r.Drv.TypeText(ctx, text, driver.ActionOpts{NoChangeMs: 300})
	if err != nil {
		r.tracef("the device failed to type: %v", err)
	} else {
		r.tracef("typed: %s", actionDesc(res))
	}
	return err
}

// typeFirstKey types text's first character and waits (on change events, up to 1 s) for the
// focused field's text to change; if it never does, the key was lost, and it is typed again
// once. It returns the rest of the text to type.
func (r *Runner) typeFirstKey(ctx context.Context, text string) (string, error) {
	first := string([]rune(text)[0])
	rest := string([]rune(text)[1:])
	before, ok := r.focusedState(ctx)
	if !ok {
		return text, nil // no field to watch: type as before
	}
	for attempt := 0; attempt < 2; attempt++ {
		r.invalidate()
		if _, err := r.Drv.TypeText(ctx, first, driver.ActionOpts{NoChangeMs: 300}); err != nil {
			return "", err
		}
		if r.awaitFieldChange(ctx, before, time.Second) {
			return rest, nil
		}
		if attempt == 0 {
			r.tracef("the first key did not reach the field (it was still connecting to the keyboard): typing it again")
		}
	}
	return rest, nil // still unchanged: a field that shows nothing (a hidden or formatted one)
}

// focusedState names the field with input focus and its text, read now: a key that lands
// changes it (its text, or which field has focus: a one-digit code box moves to the next).
func (r *Runner) focusedState(ctx context.Context) (string, bool) {
	r.invalidate()
	s, err := r.read(ctx)
	if err != nil {
		return "", false
	}
	f := focusedField(s)
	if f == nil || !f.Editable {
		return "", false
	}
	text := f.Text
	if f.ShowingHint {
		text = ""
	}
	return fmt.Sprintf("%s#%s@%v:%s", f.Class, f.ID, f.Bounds, text), true
}

// awaitFieldChange waits on change events until the focused field (or its text) is no longer
// before.
func (r *Runner) awaitFieldChange(ctx context.Context, before string, timeout time.Duration) bool {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	for {
		if t, ok := r.focusedState(ctx); !ok || t != before {
			return true // a different field, its text, or no field at all: the key went somewhere
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		r.waitAny(ctx, events, r.Drv.Seq(), deadline)
	}
}

// focusedField is the field with input focus on s, if any.
func focusedField(s *ui.Screen) *ui.Node {
	for _, n := range s.Nodes {
		if n.Focused && n.Editable {
			return n
		}
	}
	for _, n := range s.Nodes {
		if n.Focused {
			return n
		}
	}
	return nil
}

// ---- tap ----

func (r *Runner) point(spec string, within ui.Rect, elementRelative bool) (int, int, error) {
	a, b, ok := strings.Cut(spec, ",")
	if !ok {
		return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q", spec)}
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.HasSuffix(a, "%") || strings.HasSuffix(b, "%") {
		// Fractions are fine ("12.5%"): Maestro takes them too.
		px, e1 := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(a, "%")), 64)
		py, e2 := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(b, "%")), 64)
		if e1 != nil || e2 != nil || px < 0 || px > 100 || py < 0 || py > 100 || math.IsNaN(px) || math.IsNaN(py) {
			return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q: percentages are 0-100", spec)}
		}
		return within.L + int(float64(within.W())*px/100), within.T + int(float64(within.H())*py/100), nil
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
	var tappedOn *ui.Screen
	var tappedSel *match.Selector
	var last driver.ActionResult
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
		n, s, err := r.findUnderKeyboard(ctx, sel, r.lookupTimeout(c.Optional), settle.Timeout)
		if err != nil {
			return err
		}
		if loadingCover(s, n) != nil {
			n, s = r.awaitUncovered(ctx, sel, n, s, r.lookupTimeout(c.Optional))
		}
		if !n.Enabled && sel.Enabled == nil {
			t := time.Now()
			r.tracef("it is disabled: waiting up to %s for it to turn on", disabledWait)
			n, s = r.awaitEnabled(ctx, sel, n, s, disabledWait)
			if n.Enabled {
				r.tracef("it turned on after %s", since(t))
			} else {
				r.tracef("still disabled after %s: tapping anyway", since(t))
			}
		}
		tapped, tappedOn, tappedSel = n, s, sel
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
		if hc, ok := d.(hitChecker); ok {
			// The web: what the browser's own hit test reaches at the point. Something else
			// there (a veil, a dialog's backdrop, a toast) would take the tap: wait for it to go.
			if n2, x2, y2, err := r.awaitHitClear(ctx, hc, sel, n, x, y, c.Point == "", r.lookupTimeout(c.Optional)); err != nil {
				return err
			} else {
				n, x, y = n2, x2, y2
				tapped = n
			}
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
			r.tracef("%s at (%d, %d) failed: %v", tapWord(c), x, y, err)
			return err
		}
		r.tracef("%s at (%d, %d): %s", tapWord(c), x, y, actionDesc(res))
		last = res
		if res.Settle != driver.NoChange {
			if settle.React {
				r.unsettled = settleFor
			}
			break
		}
		if i+1 < attempts {
			r.tracef("retrying: nothing changed (retryTapIfNoChange)")
		}
	}
	if d.Platform() == "ios" && last.Settle == driver.NoChange && tapped != nil && tapped.Class == "Button" &&
		tapped.Enabled && c.Point == "" && !c.LongPress && c.Repeat <= 1 && systemAlertOf(tappedOn, tapped) == nil {
		return r.retrySwallowedTap(ctx, tappedSel, settle, r.lookupTimeout(c.Optional), tappedOn)
	}
	if alert := systemAlertOf(tappedOn, tapped); alert != nil && !c.LongPress && c.Repeat <= 1 {
		return r.confirmAlertTap(ctx, alert, tapped, x, y, settle)
	}
	return nil
}

// retrySwallowedTap: an iOS button shows every touch it gets (UIKit highlights it, SwiftUI
// dims it), which the device reports as a change. A tap on one that changed nothing at all
// may never have reached it: something tb cannot see took it, such as a loading veil hidden
// from accessibility. Then tap again once the screen changes (the veil going), until a tap
// gets a reaction or timeout runs out. A second tap is only made while what the screen lists
// (every element's text, value and place) is exactly as it was before the first: if anything
// changed the tap may have landed (a screen that never stops moving filters out its reaction),
// and tapping again would act twice.
func (r *Runner) retrySwallowedTap(ctx context.Context, sel *match.Selector, o driver.ActionOpts, timeout time.Duration, before *ui.Screen) error {
	if before == nil {
		return nil
	}
	sig := screenSig(before)
	unchanged := func() bool {
		r.invalidate()
		s, err := r.read(ctx)
		if err != nil {
			return false
		}
		if screenSig(s) != sig {
			r.tracef("what the screen lists changed (%s): the tap may have landed, so not tapping again", screenDiff(before, s))
			return false
		}
		return true
	}
	if !unchanged() {
		return nil
	}
	r.tracef("nothing reacted to the tap (a button shows every touch) and nothing on screen changed: something hidden may have taken it; tapping again when the screen changes")
	start := time.Now()
	deadline := start.Add(timeout)
	events, stop := r.Drv.Events()
	defer stop()
	for tries := 1; ; tries++ {
		seq := r.Drv.Seq()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(swallowedGap):
		}
		r.waitAny(ctx, events, seq, deadline)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			r.tracef("no tap reached it in %s (%d taps)", since(start), tries)
			return nil
		}
		if !unchanged() {
			return nil
		}
		n, _, err := r.findStable(ctx, sel, 0, o.Timeout)
		if err != nil {
			r.tracef("it went (%v): the last tap may have landed after all", err)
			return nil
		}
		x, y := n.Bounds.CX(), n.Bounds.CY()
		r.invalidate()
		res, err := r.Drv.Tap(ctx, x, y, o)
		if err != nil {
			return err
		}
		r.tracef("tap at (%d, %d) again: %s", x, y, actionDesc(res))
		if res.Settle != driver.NoChange {
			r.unsettled = 5 * time.Second
			return nil
		}
	}
}

// screenDiff names the first element that differs between two reads, for the trace.
func screenDiff(a, b *ui.Screen) string {
	for i := 0; i < len(a.Nodes) && i < len(b.Nodes); i++ {
		if nodeSig(a.Nodes[i]) != nodeSig(b.Nodes[i]) {
			return fmt.Sprintf("%s became %s", nodeDesc(a.Nodes[i]), nodeDesc(b.Nodes[i]))
		}
	}
	return fmt.Sprintf("%d elements, then %d", len(a.Nodes), len(b.Nodes))
}

func nodeSig(n *ui.Node) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%v|%v|%v|%v", n.Class, n.ID, n.Text, n.Desc, n.Value, n.Bounds, n.Enabled, n.Checked, n.Selected)
}

// screenSig sums up what a screen lists: each element's kind, id, text, value, place and state.
func screenSig(s *ui.Screen) uint64 {
	h := fnv.New64a()
	for _, n := range s.Nodes {
		fmt.Fprintln(h, nodeSig(n))
	}
	return h.Sum64()
}

// swallowedGap: the least time between taps on a button that did not react.
const swallowedGap = 300 * time.Millisecond

// systemAlertOf is the system alert holding n (an iOS permission prompt, which SpringBoard
// shows in a system window), or nil.
func systemAlertOf(s *ui.Screen, n *ui.Node) *ui.Node {
	if s == nil || n == nil || !systemWindow(s, n) {
		return nil
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Class == "Alert" {
			return p
		}
	}
	return nil
}

// System alerts ignore touches while they animate in, so a tap made just as one appears can be
// lost, and the alert then stays up and blocks every later step (and flow). A system alert's
// buttons always close it, so one still showing the same button after the tap means the tap
// was lost: tap again, up to alertRetries times. "Still showing" takes two reads at least
// alertRecheck apart, so an alert that is closing (and still listed for a moment) is not
// tapped again: that tap would land on the app behind it.
const (
	alertRetries = 3
	alertRecheck = 300 * time.Millisecond
)

func (r *Runner) confirmAlertTap(ctx context.Context, alert, button *ui.Node, x, y int, o driver.ActionOpts) error {
	for try := 1; ; try++ {
		r.Drv.WaitSettled(ctx, 2*time.Second)
		if !r.alertStillUp(ctx, alert, button) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(alertRecheck * time.Duration(try)):
		}
		if !r.alertStillUp(ctx, alert, button) {
			return nil
		}
		if try > alertRetries {
			r.tracef("the system alert %q is still up after %d taps on %q: leaving it", alert.Label(), try, button.Label())
			return nil
		}
		r.tracef("the system alert %q is still up: the tap on %q was lost (alerts ignore touches while they appear); tapping again", alert.Label(), button.Label())
		r.invalidate()
		res, err := r.Drv.Tap(ctx, x, y, o)
		if err != nil {
			return err
		}
		r.tracef("tap at (%d, %d): %s", x, y, actionDesc(res))
	}
}

// closeStaleAlerts closes system alerts still up before a launch that restarts the app (iOS).
// One left from before (a permission prompt the last flow did not answer: it ended, or failed,
// first) outlives the app that raised it, even a reinstall, and would take every touch of the
// flow being started, so this flow, and each after it, would fail. Nothing the new launch does
// can have raised it yet. Each is declined (its "Don't Allow" or "Cancel", else its first
// button): launchApp sets the permissions the flow wants itself.
func (r *Runner) closeStaleAlerts(ctx context.Context) error {
	if r.Drv.Platform() == "android" {
		// The system dialogs a lookup answers (an autofill save sheet left up, an "isn't
		// responding" dialog) would keep the app from coming to the front.
		for i := 0; i < maxDialogAnswers; i++ {
			r.invalidate()
			s, err := r.read(ctx)
			if err != nil {
				return nil
			}
			if tapped, err := r.answerDialog(ctx, s); err != nil || !tapped {
				return err
			}
			r.Drv.WaitSettled(ctx, 2*time.Second)
		}
		return nil
	}
	if r.Drv.Platform() != "ios" {
		return nil
	}
	for i := 0; i < maxDialogAnswers; i++ {
		r.invalidate()
		s, err := r.read(ctx)
		if err != nil {
			return nil // nothing to go on: launch as before
		}
		var alert, button *ui.Node
		for _, n := range s.Nodes {
			if n.Class == "Alert" && systemWindow(s, n) {
				alert = n
				break
			}
		}
		if alert == nil {
			return nil
		}
		alert.Walk(func(n *ui.Node) bool {
			if n.Class != "Button" {
				return true
			}
			l := strings.ToLower(strings.ReplaceAll(n.Label(), "’", "'"))
			if strings.Contains(l, "don't allow") || l == "cancel" || l == "not now" || l == "deny" {
				button = n
				return false
			}
			if button == nil {
				button = n
			}
			return true
		})
		if button == nil {
			r.tracef("a system alert %q from before is still up, with no button to close it", alert.Label())
			return nil
		}
		r.tracef("a system alert from before is still up: %q: tapping %q before launching", alert.Label(), button.Label())
		if _, err := r.Drv.Tap(ctx, button.Bounds.CX(), button.Bounds.CY(), driver.ActionOpts{Hold: iosFrameHold}); err != nil {
			return err
		}
		r.Drv.WaitSettled(ctx, 2*time.Second)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(alertRecheck):
		}
	}
	return nil
}

// systemWindow: n is in a window of the system's, not an app's: a system window, or
// SpringBoard's (iOS lists SpringBoard as the app when no app is in front, with its alerts).
func systemWindow(s *ui.Screen, n *ui.Node) bool {
	if n.Window < 0 || n.Window >= len(s.Windows) {
		return false
	}
	w := s.Windows[n.Window]
	return w.Type == "system" || w.Package == "com.apple.springboard"
}

// alertStillUp: a fresh read still shows the alert with the button, where it was.
func (r *Runner) alertStillUp(ctx context.Context, alert, button *ui.Node) bool {
	r.invalidate()
	s, err := r.read(ctx)
	if err != nil {
		return false
	}
	for _, n := range s.Nodes {
		if n.Class != button.Class || n.Label() != button.Label() || n.Bounds != button.Bounds {
			continue
		}
		if a := systemAlertOf(s, n); a != nil && a.Label() == alert.Label() {
			return true
		}
	}
	return false
}

// findUnderKeyboard finds a tap's target. One the keyboard hides is often not listed at all
// (Android leaves out views it covers): when it is not found within keyboardGrace while the
// keyboard is up, the keyboard is hidden, as a person would, and the lookup goes on. The grace
// lets something about to appear above the keyboard (a suggestion) be found first.
func (r *Runner) findUnderKeyboard(ctx context.Context, sel *match.Selector, timeout, settle time.Duration) (*ui.Node, *ui.Screen, error) {
	if timeout <= keyboardGrace {
		return r.findStable(ctx, sel, timeout, settle)
	}
	start := time.Now()
	n, s, err := r.findStable(ctx, sel, keyboardGrace, settle)
	var nf *NotFound
	if err == nil || !errors.As(err, &nf) || ctx.Err() != nil {
		return n, s, err // found, or a failure that looking longer would not mend (a lost device)
	}
	if s != nil && s.Keyboard {
		r.tracef("not found while the keyboard is up: hiding it (it may cover the element) and looking again")
		if herr := r.Drv.HideKeyboard(ctx); herr != nil {
			r.tracef("hiding the keyboard failed: %v", herr)
		}
		r.invalidate()
	}
	return r.findStable(ctx, sel, max(timeout-time.Since(start), 0), settle)
}

// keyboardGrace: how long a tap looks for its target with the keyboard up before hiding it.
const keyboardGrace = 1500 * time.Millisecond

// tapWord names a tap command's gesture for the trace.
func tapWord(c *flow.Command) string {
	switch {
	case c.LongPress:
		return "long press"
	case c.Repeat > 1 && c.Name == "doubleTapOn":
		return "double tap"
	case c.Repeat > 1:
		return fmt.Sprintf("%d taps", c.Repeat)
	}
	return "tap"
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

// busyIndicator: a spinner or a progress bar (Android's ProgressBar, which Compose's progress
// indicators report as too).
func busyIndicator(n *ui.Node) bool {
	return strings.HasSuffix(n.Class, "ProgressBar")
}

// loadingCover is a loading overlay over n, or nil (Android, iOS). Apps lay one over a screen while
// it loads, often see-through: n shows through it and stays in the tree, but the overlay takes
// the touches, and a tap on n is lost. It is a node at n's center, outside n and not one of
// its containers, that is a spinner or holds one. Android lists views by place on screen, not
// as drawn, so the order says nothing; screens hidden under the current one are not listed.
//
// iOS: a veil's spinner is listed with the veil's frame (SwiftUI gives it the overlay's
// size), the rest of the veil being hidden from accessibility. A spinner whose frame holds the
// whole of n is such a veil; a spinner of its own size, beside n, is not.
func loadingCover(s *ui.Screen, n *ui.Node) *ui.Node {
	if s.Platform == "ios" {
		for _, c := range s.Nodes {
			b := c.Bounds
			if c != n && c.Class == "ActivityIndicator" && b.L <= n.Bounds.L && b.T <= n.Bounds.T && b.R >= n.Bounds.R &&
				b.B >= n.Bounds.B && !c.IsDescendantOf(n) && !n.IsDescendantOf(c) {
				return c
			}
		}
		return nil
	}
	if s.Platform != "android" {
		return nil
	}
	x, y := n.Bounds.CX(), n.Bounds.CY()
	for _, c := range s.Nodes {
		if c == n || c.Window != n.Window || !c.Bounds.Contains(x, y) || c.IsDescendantOf(n) || n.IsDescendantOf(c) {
			continue
		}
		if busyIndicator(c) {
			return c
		}
		found := false
		c.Walk(func(d *ui.Node) bool {
			if !found && d != c && busyIndicator(d) {
				found = true
			}
			return !found
		})
		if found {
			return c
		}
	}
	return nil
}

// awaitUncovered waits, on change events, for a loading overlay over n to go (up to timeout;
// then n is tapped anyway), lets the screen settle, and finds n again where it rests.
func (r *Runner) awaitUncovered(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, timeout time.Duration) (*ui.Node, *ui.Screen) {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	start := time.Now()
	r.tracef("under a loading overlay (%s): waiting up to %s for it to go", nodeDesc(loadingCover(s, n)), timeout.Round(time.Millisecond))
	for loadingCover(s, n) != nil && time.Now().Before(deadline) {
		r.waitAny(ctx, events, s.Seq, deadline)
		if ctx.Err() != nil {
			break
		}
		r.invalidate()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			continue
		}
		n, s = n2, s2
	}
	if loadingCover(s, n) != nil {
		r.tracef("the overlay is still there after %s: tapping anyway", since(start))
	} else {
		r.tracef("the overlay went after %s", since(start))
	}
	r.Drv.WaitSettled(ctx, 2*time.Second)
	r.invalidate()
	if n2, s2, _, err := r.find(ctx, sel, 0); err == nil {
		n, s = n2, s2
	}
	return n, s
}

// hitChecker is a driver that can say what a tap at a point would reach instead of a node
// (the web: the browser's hit test).
type hitChecker interface {
	CoveredAt(ctx context.Context, index, x, y int) (string, error)
}

// awaitHitClear waits, on change events, until a tap at (x, y) would reach n (up to timeout;
// then n is tapped anyway), and finds n again where it rests. center: the point is n's middle,
// so it follows n if it moved.
func (r *Runner) awaitHitClear(ctx context.Context, hc hitChecker, sel *match.Selector, n *ui.Node, x, y int, center bool, timeout time.Duration) (*ui.Node, int, int, error) {
	what, err := hc.CoveredAt(ctx, n.Index, x, y)
	if err != nil || what == "" {
		return n, x, y, nil // a failed check does not fail the tap
	}
	start := time.Now()
	r.tracef("covered at (%d, %d) by %s: waiting up to %s for it to go", x, y, what, timeout.Round(time.Millisecond))
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	var gone error // the latest lookup's error: the target is not on screen
	r.invalidate()
	if _, _, _, err := r.find(ctx, sel, 0); err != nil {
		gone = err
	}
	for what != "" && time.Now().Before(deadline) {
		r.waitAny(ctx, events, r.Drv.Seq(), deadline)
		if ctx.Err() != nil {
			return n, x, y, ctx.Err()
		}
		r.invalidate()
		n2, _, _, err := r.find(ctx, sel, 0)
		if gone = err; err != nil {
			continue
		}
		dx, dy := x-n.Bounds.L, y-n.Bounds.T
		n = n2
		if center {
			x, y = n.Bounds.CX(), n.Bounds.CY()
		} else {
			x, y = n.Bounds.L+dx, n.Bounds.T+dy
		}
		if what, err = hc.CoveredAt(ctx, n.Index, x, y); err != nil {
			what = ""
		}
	}
	if what != "" && gone != nil {
		// The target left (the page moved: a banner pushed it out of view) and something else
		// is where it was. Tapping there would tap that instead: fail on the target.
		r.tracef("it is gone, and %s is where it was: not tapping that", what)
		return n, x, y, gone
	}
	if what != "" {
		r.tracef("still covered by %s after %s: tapping anyway", what, since(start))
	} else {
		r.tracef("uncovered after %s", since(start))
	}
	return n, x, y, nil
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
	if err != nil {
		r.tracef("the device failed it: %v", err)
	} else {
		r.tracef("%s", actionDesc(res))
	}
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
		// To near the screen's edge, and at least 70% of the element's size: a pager turns
		// a page only for a swipe past its middle (or a fast one), and one from its centre to
		// 10% of the screen falls short when the pager has margins.
		x2, y2 = x1, y1
		bw, bh := n.Bounds.W()*7/10, n.Bounds.H()*7/10
		switch c.Direction {
		case "UP":
			y2 = max(0, min(h/10, y1-bh))
		case "DOWN":
			y2 = min(h-1, max(h*9/10, y1+bh))
		case "LEFT":
			x2 = max(0, min(w/10, x1-bw))
		case "RIGHT":
			x2 = min(w-1, max(w*9/10, x1+bw))
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
	res, err := d.Swipe(ctx, x1, y1, x2, y2, dur, opts)
	if err != nil {
		r.tracef("swipe (%d, %d) -> (%d, %d) failed: %v", x1, y1, x2, y2, err)
	} else {
		r.tracef("swipe (%d, %d) -> (%d, %d) in %d ms: %s", x1, y1, x2, y2, dur.Milliseconds(), actionDesc(res))
	}
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
	swipes, scrollStart := 0, time.Now()
	nudged := false            // a fully visible element flush with an edge was brought in once
	nudgePending := false      // ...and the drag that brings it in is still to come
	tried := map[string]bool{} // vertical lists dragged so far (listKey)
	// placeVertical moves up-and-down drags (and the fallback flings) into box: from 3/4 of
	// its visible height to near its top (or the reverse), at its middle.
	placeVertical := func(box *ui.Node) {
		b := box.Bounds.Intersect(screen)
		x1, x2, fx1, fy1 = b.CX(), b.CX(), b.CX(), b.CY()
		if c.Direction == "DOWN" {
			y1, y2 = b.T+b.H()*3/4, b.T+b.H()/10
		} else {
			y1, y2 = b.T+b.H()/4, b.T+b.H()*9/10
		}
	}
	flung := false // the last swipe was a fling
	broughtIn := 0 // drags that brought a sideways list into view first
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
			vis = match.VisibleShare(n.Bounds, n.ClipRect(screen))
		}
		centering := c.CenterElement && vis > 0.1 && centerTries <= 4
		if need >= 1 && !nudged && n != nil && vis >= need && d.Platform() == "android" && flush(n.Bounds, n.ClipRect(screen), vertical) {
			// Android reports bounds already cut to what shows: a row flush with the edge of
			// its list (or the screen) may be partly hidden there. Bring it in once.
			r.tracef("it sits flush with the edge of its list: it may be cut off there; bringing it in")
			nudged, nudgePending = true, true
		}
		centering = centering || nudgePending
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
			if swipes > 0 {
				r.tracef("in view after %d swipes, %s: %s", swipes, since(scrollStart), nodeDesc(n))
			}
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
				r.tracef("the drag moved nothing (a list loading, or at its end): waiting up to 2 s for a change")
				r.waitAny(ctx, events, s.Seq, dl)
				continue
			}
		}
		stalled = time.Time{}
		if !time.Now().Before(deadline) {
			r.tracef("gave up after %d swipes, %s (fling mode: %v)", swipes, since(scrollStart), fling)
			return failf("No visible element found: %s", sel.Describe())
		}
		if fling && flung && n == nil {
			// The last fling brought new things into view: the list scrolls (it is not a
			// paging list springing back), so go back to drags, which cannot carry the element
			// past the screen. A paging list springs back again, and flings again.
			if newTexts(s, x1, y1, seen) > 0 {
				r.tracef("the fling brought new things into view: dragging again")
				fling, fruitless = false, 0
			}
		}
		flung = false
		if !fling && !dragged.IsZero() && n == nil {
			fresh := newTexts(s, x1, y1, seen)
			if fresh == 0 {
				// Nothing new yet: the app may be a frame or two behind the drag (a screen
				// that never stops moving reads as changed while the list has not moved).
				// Judge it on the settled screen, not this read: two fruitless drags switch
				// to flinging for good, and a fling can carry the element past the screen.
				d.WaitSettled(ctx, time.Second)
				r.invalidate()
				if s, err = r.read(ctx); err != nil {
					return err
				}
				if n, _, err = r.match(ctx, s, sel); err != nil {
					return &invalidField{msg: err.Error()}
				}
				if n != nil {
					dragged = time.Time{}
					continue
				}
				fresh = newTexts(s, x1, y1, seen)
			}
			if fresh == 0 {
				fruitless++
			} else {
				fruitless = 0
			}
			if fruitless >= 2 && vertical {
				// This list shows nothing new: another list on screen may hold the element
				// (the other pane of a two-pane layout). Try it before flinging this one.
				markTried(s, x1, y1, tried)
				if box := otherList(s, tried); box != nil {
					tried[listKey(box)] = true
					placeVertical(box)
					fruitless = 0
					r.tracef("2 drags brought nothing new here: trying the other list, %s", nodeDesc(box))
				}
			}
			if fruitless >= 2 && !fling {
				r.tracef("2 drags in a row brought nothing new into view (a paging list springs back): flinging from now on")
			}
			fling = fruitless >= 2
		} else if dragged.IsZero() {
			newTexts(s, x1, y1, seen)
		}
		if fling {
			// A classic swipe: it flings, and settles before the next look.
			r.invalidate()
			swipes++
			res, err := d.Swipe(ctx, fx1, fy1, x2, y2, dur, driver.ActionOpts{NoChangeMs: 300})
			if err != nil {
				return err
			}
			flung = true
			r.tracef("fling %d: (%d, %d) -> (%d, %d): %s", swipes, fx1, fy1, x2, y2, actionDesc(res))
			// A fling coasts on: like a drag, the element counts once two reads agree on
			// where it is (it can be found while the list still moves, and then carried off).
			dragged, last = time.Now(), ui.Rect{}
			before = 0
			continue
		}
		if vertical && !placed {
			// Up and down: across the list the element is listed in, when that is not the
			// one at the middle of the screen (a two-pane layout: the middle is the other
			// pane); else from the middle, as a person would.
			listed := n
			if listed == nil {
				listed, _ = match.FindListed(s, sel)
			}
			if box := ownList(s, listed, x1, y1); box != nil {
				placeVertical(box)
				placed = true
				tried[listKey(box)] = true
				r.tracef("dragging the list the element is in: %s", nodeDesc(box))
			} else {
				y1, placed = dragStart(s, d.Platform(), x1, y1, c.Direction)
				markTried(s, x1, y1, tried)
			}
		} else if !vertical && !placed {
			// Sideways: across the sideways list the element is in, or the one on screen when
			// the middle of the screen has none (a carousel above the middle): a swipe there
			// would move nothing.
			placed = true
			listed := n
			if listed == nil {
				listed, _ = match.FindListed(s, sel)
			}
			if box := sidewaysList(s, listed, w/2, h/2, sel.Text); box != nil {
				if shown := box.Bounds.Intersect(screen); shown.H()*10 < box.Bounds.H()*6 && broughtIn < 2 {
					// Mostly off screen (a shelf at the bottom edge): bring it to the middle
					// first, dragging the page, so the sideways drags run across it, not
					// along a sliver at the screen's edge.
					dy := clamp(box.Bounds.CY()-h/2, -h*4/10, h*4/10)
					r.tracef("the list to drag sideways is mostly off screen: bringing it in first")
					r.invalidate()
					broughtIn++
					if _, err := d.Swipe(ctx, w/2, h/2+dy/2, w/2, h/2-dy/2, dur, drag); err != nil {
						return err
					}
					d.WaitSettled(ctx, time.Second)
					placed = false
					continue
				}
				b := box.Bounds.Intersect(screen)
				y1, y2, fx1, fy1 = b.CY(), b.CY(), b.CX(), b.CY()
				if c.Direction == "RIGHT" {
					x1, x2 = b.L+b.W()*85/100, b.L+b.W()*15/100
				} else {
					x1, x2 = b.L+b.W()*15/100, b.L+b.W()*85/100
				}
			}
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
		nudgePending = false
		swipes++
		if swipes == 1 && !vertical {
			r.tracef("dragging sideways at y=%d (x %d -> %d)", y1, x1, tx)
		}
		if _, err := d.Swipe(ctx, x1, y1, tx, ty, dur, drag); err != nil {
			return err
		}
		if n != nil {
			r.tracef("drag %d: (%d, %d) -> (%d, %d); target listed but %.0f%% visible", swipes, x1, y1, tx, ty, vis*100)
		} else {
			r.tracef("drag %d: (%d, %d) -> (%d, %d); target not on screen yet, %d texts seen", swipes, x1, y1, tx, ty, len(seen))
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
// ownList is the up-and-down scrolling list n is listed in (off screen), when that is not the
// list at (x, y), the middle of the screen; else nil.
func ownList(s *ui.Screen, n *ui.Node, x, y int) *ui.Node {
	if n == nil {
		return nil
	}
	screen := ui.Rect{R: s.Width, B: s.Height}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && !sidewaysShaped(p, screen) && !p.Bounds.Intersect(screen).Empty() {
			if p.Bounds.Contains(x, y) {
				return nil // the middle's list: dragged there anyway
			}
			return p
		}
	}
	return nil
}

// markTried records every scrolling list under (x, y) as tried: the drags there moved them.
func markTried(s *ui.Screen, x, y int, tried map[string]bool) {
	for _, n := range s.Nodes {
		if n.Scrollable && n.Bounds.Contains(x, y) {
			tried[listKey(n)] = true
		}
	}
}

// listKey names a list across reads (each read makes new nodes): its kind, id and left edge
// and width (a list's top and height stay, its content moves).
func listKey(n *ui.Node) string {
	return fmt.Sprintf("%s#%s@%d+%d", n.Class, n.ID, n.Bounds.L, n.Bounds.W())
}

// otherList is the largest up-and-down scrolling list on screen not tried yet (a list that
// is not sideways-shaped and shows at least a fifth of the screen's height), or nil.
func otherList(s *ui.Screen, tried map[string]bool) *ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	var best *ui.Node
	for _, c := range s.Nodes {
		if !c.Scrollable || tried[listKey(c)] || sidewaysShaped(c, screen) || c.Parent == nil {
			continue
		}
		b := c.Bounds.Intersect(screen)
		if b.H()*5 < s.Height || b.W() > 2*b.H() && b.H() < s.Height/3 {
			continue
		}
		if best == nil || b.Area() > best.Bounds.Intersect(screen).Area() {
			best = c
		}
	}
	return best
}

// sidewaysShaped: a scrolling list that scrolls sideways (Android's HorizontalScrollView, or
// one much wider than tall on screen: a carousel, a shelf).
func sidewaysShaped(c *ui.Node, screen ui.Rect) bool {
	if strings.Contains(c.Class, "HorizontalScrollView") {
		return true
	}
	b := c.Bounds.Intersect(screen)
	return b.W() > 2*b.H() && b.H() > 0
}

// sidewaysList is the list a sideways scrollUntilVisible should drag, when it is not the one
// at (x, y), the middle of the screen: the sideways list holding n (listed but off screen),
// else the sideways list on screen whose items look most like the text looked for (shelves
// of "Card 6-1", "Card 6-2"... for "Card 6-12": lazy lists list only what shows), else the
// largest. nil: drag at (x, y), as before.
func sidewaysList(s *ui.Screen, n *ui.Node, x, y int, want string) *ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	sideways := func(c *ui.Node) bool { return c.Scrollable && sidewaysShaped(c, screen) }
	if n != nil {
		for p := n.Parent; p != nil; p = p.Parent {
			if sideways(p) {
				return p
			}
		}
	}
	var lists []*ui.Node
	for _, c := range s.Nodes {
		if sideways(c) && !c.Bounds.Intersect(screen).Empty() {
			lists = append(lists, c)
		}
	}
	if want != "" {
		best, bestScore := (*ui.Node)(nil), 0
		for _, c := range lists {
			score := 0
			c.Walk(func(d *ui.Node) bool {
				score = max(score, commonPrefix(d.Label(), want))
				return true
			})
			if score > bestScore {
				best, bestScore = c, score
			}
		}
		// A shared prefix longer than any other list's, and more than a word's start.
		if best != nil && bestScore >= 3 && uniqueBest(lists, best, bestScore, want) {
			return best
		}
	}
	if box := scrollContainer(s, x, y); box != nil && sideways(box) {
		return nil
	}
	var largest *ui.Node
	for _, c := range lists {
		if largest == nil || c.Bounds.Intersect(screen).Area() > largest.Bounds.Intersect(screen).Area() {
			largest = c
		}
	}
	return largest
}

// uniqueBest: no other list's items share as long a prefix with want as best's.
func uniqueBest(lists []*ui.Node, best *ui.Node, score int, want string) bool {
	for _, c := range lists {
		if c == best {
			continue
		}
		other := 0
		c.Walk(func(d *ui.Node) bool {
			other = max(other, commonPrefix(d.Label(), want))
			return true
		})
		if other >= score {
			return false
		}
	}
	return true
}

// commonPrefix is the length of the common start of a and b (case-sensitive bytes).
func commonPrefix(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

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
	screen := ui.Rect{R: s.Width, B: s.Height}
	add := func(nd *ui.Node) bool {
		// Only what shows: a list that lists all its rows lists the far ones too.
		if nd.Bounds.Intersect(nd.ClipRect(screen)).Empty() {
			return true
		}
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

// flush: b touches the clip's edge along the scroll axis (within a pixel).
func flush(b, clip ui.Rect, vertical bool) bool {
	if vertical {
		return abs(b.T-clip.T) <= 1 || abs(b.B-clip.B) <= 1
	}
	return abs(b.L-clip.L) <= 1 || abs(b.R-clip.R) <= 1
}

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
		r.tracef("condition not met (%s): skipped", describeCondition(c.When))
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
				r.tracef("repeat: the while condition no longer holds after %d times", n)
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
		if attempt < max {
			r.tracef("retry: attempt %d of %d failed (%v): trying again", attempt+1, max+1, err)
		}
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
