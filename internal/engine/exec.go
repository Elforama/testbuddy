package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
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
	if c.Name != "tapOn" {
		r.previousGuardedTap = nil
	}
	switch c.Name {
	case "eraseText":
		if d.Platform() != "ios" {
			r.inputTarget = nil
		}
	case "inputText", "pasteText", "inputRandomText", "inputRandomNumber", "inputRandomEmail", "inputRandomPersonName", "inputRandomCityName", "inputRandomCountryName", "inputRandomColorName",
		"assertVisible", "assertNotVisible", "extendedWaitUntil", "waitForAnimationToEnd":
	default:
		r.inputTarget = nil
	}
	switch c.Name {
	case "launchApp", "stopApp", "killApp", "clearState", "openLink", "back":
		r.forgetHistory()
	}
	switch c.Name {
	case "tapOn", "doubleTapOn", "longPressOn":
		err := r.tap(ctx, c)
		if err != nil {
			r.inputTarget = nil
		}
		return "", err

	case "assertVisible":
		sel, err := r.resolveSelector(c.Selector)
		if err != nil {
			return "", err
		}
		_, _, _, err = r.findWithChangeRead(ctx, sel, r.adjusted(r.opts.LookupTimeout), true)
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
		res, err := d.EraseText(ctx, c.Count, driver.ActionOpts{NoChangeMs: 300, Target: r.inputTarget})
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
		r.mark("swipe", [2]int{w / 2, h / 2}, [2]int{w / 2, h / 10})
		_, err := d.Swipe(ctx, w/2, h/2, w/2, h/10, dur, driver.ActionOpts{})
		return "", err

	case "scrollUntilVisible":
		return "", r.scrollUntilVisible(ctx, c)

	case "scrollUntilVisibleIn":
		return "", r.runList(ctx, c.Commands, f, nil)

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
		// The run's recording (--record) gives the device to the flow's until stopRecording.
		r.recordStop()
		if err := d.StartRecording(ctx, out); err != nil {
			defer r.recordStart(ctx)
			if d.Platform() == "web" {
				// A recording is a debugging aid, not part of what a flow checks: on the web
				// (which needs ffmpeg to record) the flow goes on without one.
				r.logLine("the screen is not recorded: " + filepath.Base(out) + ": " + err.Error())
				return "", nil
			}
			return "", err
		}
		r.recording = true
		r.rec.flowPath, r.rec.flowStart = out, time.Now()
		return "", nil

	case "stopRecording":
		if !r.recording {
			return "", nil
		}
		err := r.stopFlowRecording(ctx)
		r.recordStart(ctx)
		return "", err

	case "waitForAnimationToEnd":
		t, err := r.eval(c.Timeout)
		if err != nil {
			return "", err
		}
		timeout, err := parseMs(t, 15*time.Second)
		if err != nil || timeout <= 0 {
			timeout = 15 * time.Second
		}
		if how := waitForAnimation(ctx, d, timeout); how != "" {
			r.tracef("%s", how)
		}
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

	case "mockNetwork":
		return "", r.mockNetwork(ctx, c)

	case "clearNetworkMocks":
		return "", r.clearNetworkMocks(ctx)

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
