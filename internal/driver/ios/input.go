package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

func actionParams(o driver.ActionOpts, p map[string]any) map[string]any {
	if o.NoSettle {
		p["settle"] = false
	}
	if o.React {
		p["react"] = true
	}
	if o.NoChangeMs > 0 {
		p["noChange"] = o.NoChangeMs
	} else {
		// As on Android: a tap that opens a screen can take most of a second to start changing
		// it (React Native runs a press on its JavaScript thread while the main run loop is
		// idle). Taken for "nothing happened" sooner, the next command could read the screen
		// the tap was leaving. When it does change, the wait ends at once.
		p["noChange"] = 1000
	}
	if o.Timeout > 0 {
		p["timeout"] = o.Timeout.Milliseconds()
	}
	return p
}

func (d *Driver) action(ctx context.Context, op string, o driver.ActionOpts, p map[string]any, extra time.Duration) (driver.ActionResult, error) {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if t := o.Target; t != nil {
		p["targetID"], p["targetPID"] = t.ID, t.Window
		p["targetSeq"] = d.runnerSeq(t.Seq)
		if t.Moving {
			p["targetMoving"] = true
		}
	}
	limit := timeout + extra + 15*time.Second
	typing := op == "type" || op == "erase"
	if typing && ci.On() {
		// The agent cannot read the host's environment: CI mode decides there how a field
		// gets the focus back (a touch at its middle, issue #46; XCTest's tap elsewhere).
		p["ci"] = true
	}
	ask := func() (*wire.Msg, error) {
		if boundedOps[op] {
			// Issues #46, #130: the agent answers before this, with an error that says what it
			// waited for, instead of the host timing out blind on an app that does not answer.
			p["replyMs"] = replyLimit(ctx, limit).Milliseconds()
		}
		return d.call(ctx, op, actionParams(o, p), limit)
	}
	m, err := ask()
	if why := notStarted(err); boundedOps[op] && why != "" && ci.On() {
		// A CI allowance (package ci): the app did not answer accessibility in time (its main
		// thread busy, as on a slow runner bringing up the keyboard, issues #46, #130). In CI it
		// is asked once more; elsewhere the agent's error, which says what it waited for, fails
		// the step. Nothing was sent: asking once more cannot type or press twice. A new
		// request (a new token), as the first one has its answer.
		d.note(fmt.Sprintf("%s: the agent gave up before sending anything (%s); asking once more", op, why))
		m, err = ask()
	}
	if err != nil {
		return driver.ActionResult{}, err
	}
	var r struct {
		Result    string `json:"result"`
		Withheld  string `json:"withheld"`
		Cancelled bool   `json:"cancelled"`
		Touch     string `json:"touch"`
		Guarded   bool   `json:"guarded"`
		Probed    bool   `json:"probed"`
		Targeted  bool   `json:"targeted"`
		Clicked   *bool  `json:"clicked"`
		InjectMs  int    `json:"injectMs"`
		SettleMs  int    `json:"settleMs"`
		Seq       int64  `json:"seq"`
	}
	if err := m.Decode(&r); err != nil {
		return driver.ActionResult{}, err
	}
	if r.Withheld != "" && (op != "tap" || o.Target == nil || !r.Guarded || (r.Withheld != "preflight" && r.Withheld != "delivery" && r.Withheld != "moved") || r.Clicked != nil) {
		return driver.ActionResult{}, fmt.Errorf("invalid tap delivery receipt %q", r.Withheld)
	}
	// Issue #131: a guarded, probed tap's touch that ended cancelled. Never with a click (that
	// touch did act) or a withheld touch (that one never reached the app).
	if r.Cancelled && (op != "tap" || o.Target == nil || !r.Guarded || !r.Probed || r.Withheld != "" || r.Clicked != nil) {
		return driver.ActionResult{}, errors.New("invalid tap delivery receipt: cancelled")
	}
	return driver.ActionResult{Withheld: r.Withheld, Cancelled: r.Cancelled, Touch: r.Touch, Guarded: r.Guarded, Probed: r.Probed, Targeted: r.Targeted, Clicked: r.Clicked, Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: d.seq(r.Seq)}, nil
}

// boundedOps are the actions the agent answers before the host's reply limit (replyMs) whatever
// the app does: type and erase (issue #46), key and hideKeyboard (issue #130). (The agent's
// clear op is bounded too, but the host never sends it.)
var boundedOps = map[string]bool{"type": true, "erase": true, "key": true, "hideKeyboard": true}

// notStarted is the agent's reason when err says a bounded op (or a screen read) gave up on
// the app before sending anything ("" otherwise): it marks only such errors transient. Keys
// that may have gone are never sent again.
func notStarted(err error) string {
	var agent *wire.AgentError
	if errors.As(err, &agent) && agent.Transient {
		return agent.Msg
	}
	return ""
}

func (d *Driver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	p := map[string]any{"x": x, "y": y}
	if o.Hold > 0 {
		p["hold"] = o.Hold.Milliseconds()
	}
	return d.tapAction(ctx, "tap", x, y, o, p)
}

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.tapAction(ctx, "doubleTap", x, y, o, map[string]any{"x": x, "y": y})
}

// Two taps on one spot are two taps, not a double tap (issue #212). UIKit takes a touch that
// comes down within 350 ms of the last one, near it, as that tap's second: a WKWebView's
// double-tap zoom did, for two tapOn steps on a page's button, and lost a click. So a tap or
// double tap near the last one is sent only once doubleTapWindow has passed since the last one
// lifted (measured from its confirmation, which comes after the lift reached the app, so the
// gap the app sees is at least as long). doubleTapWindow leaves a margin over UIKit's 350 ms.
// doubleTapNear: measured on an iPhone 17 Pro simulator (iOS 27), on the bench's WKWebView
// page: a tap on its button and one 50 or 80 pt from it, about 250 ms apart in either order,
// never zoomed or lost the click (16 of 16), so 60 pt covers taps on one spot and leaves
// neighbouring keys and segments alone. A tap elsewhere does not wait, nor does a
// tap of a tapOn's repeat after its first (the flow's delay times those: o.InRepeat).
//
// Not a CI allowance: UIKit's double-tap window is the same on any machine, and #212 showed
// locally too (on an idle simulator a WebView pair zoomed in 2 of 10 runs, and a loop of
// pairs lost a tap in 4 of 4 runs).
const (
	doubleTapWindow = 450 * time.Millisecond
	doubleTapNear   = 60 // points
)

// tapAction sends a tap or double tap, after waiting out the double-tap window of the last one
// when it is near it.
func (d *Driver) tapAction(ctx context.Context, op string, x, y int, o driver.ActionOpts, p map[string]any) (driver.ActionResult, error) {
	d.tapMu.Lock()
	defer d.tapMu.Unlock()
	if last := d.lastTap; !last.at.IsZero() && !o.InRepeat {
		dx, dy := x-last.x, y-last.y
		if dx*dx+dy*dy <= doubleTapNear*doubleTapNear {
			if wait := doubleTapWindow - time.Since(last.at); wait > 0 {
				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					return driver.ActionResult{}, ctx.Err()
				case <-t.C:
				}
			}
		}
	}
	res, err := d.action(ctx, op, o, p, 0)
	// Recorded even after an error (the touch may have gone), a cancelled ctx included: that
	// costs at most one wait of doubleTapWindow before a later tap near this one.
	at := time.Now()
	if err == nil {
		// The agent's settle wait came after the touch was confirmed: it counts toward the gap.
		at = at.Add(-time.Duration(res.SettleMs) * time.Millisecond)
	}
	d.lastTap = lastTap{x: x, y: y, at: at}
	return res, err
}

// lastTap is where the last tap or double tap went, and when it was confirmed (zero after a
// long press or swipe, which ends UIKit's tap sequence).
type lastTap struct {
	x, y int
	at   time.Time
}

// endTaps forgets the last tap: a long press or swipe came after it.
func (d *Driver) endTaps() {
	d.tapMu.Lock()
	d.lastTap = lastTap{}
	d.tapMu.Unlock()
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	defer d.endTaps()
	return d.action(ctx, "press", o, map[string]any{"x": x, "y": y, "ms": hold.Milliseconds()}, hold)
}

// How long a resting swipe's finger stays still before it moves, and creeps the last few points
// before it lifts (the agent's Gestures.swipeRestS and swipeEndCreepS; issues #127, #182): the
// host allows for both in the reply limit.
const (
	swipeRest     = 200 * time.Millisecond
	swipeEndCreep = 1500 * time.Millisecond
)

func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	defer d.endTaps()
	p := map[string]any{"x1": x1, "y1": y1, "x2": x2, "y2": y2, "ms": dur.Milliseconds()}
	if o.Drag {
		p["drag"] = true
	}
	if ci.On() {
		// The agent's wait for SpringBoard before a swipe or drag is a CI allowance (issue
		// #137, seen on a CI runner only): the agent cannot read the host's environment.
		p["ci"] = true
	}
	if o.Rest {
		p["rest"] = true
		dur += swipeRest + swipeEndCreep
	}
	return d.action(ctx, "swipe", o, p, dur)
}

func (d *Driver) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	// 40 ms a character (issue #130: not a byte; "こんにちは" is 5 characters in 15 bytes).
	return d.action(ctx, "type", o, map[string]any{"text": text}, time.Duration(utf8.RuneCountInString(text))*40*time.Millisecond)
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "erase", o, map[string]any{"n": n}, time.Duration(n)*20*time.Millisecond)
}

// The flow format's key names → the Android key codes the protocol carries; the runner maps them to iOS.
var keyCodes = map[string]int{
	"enter": 66, "backspace": 67, "delete": 67, "home": 3, "lock": 26, "power": 26,
	"volume up": 24, "volume down": 25, "tab": 61, "escape": 111, "back": 4,
}

// KeyCode maps a flow key name to the code the runner takes.
func KeyCode(key string) (int, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "_", " ")
	c, ok := keyCodes[k]
	return c, ok
}

func (d *Driver) PressKey(ctx context.Context, key string, o driver.ActionOpts) (driver.ActionResult, error) {
	code, ok := KeyCode(key)
	if !ok {
		return driver.ActionResult{}, fmt.Errorf("key %q is not supported on iOS", key)
	}
	return d.action(ctx, "key", o, map[string]any{"code": code}, 0)
}

func (d *Driver) HideKeyboard(ctx context.Context) error {
	_, err := d.action(ctx, "hideKeyboard", driver.ActionOpts{}, map[string]any{}, 5*time.Second)
	return err
}

func (d *Driver) Screenshot(ctx context.Context, scale float64, format string) ([]byte, error) {
	if format == "" {
		format = "png"
	}
	m, err := d.call(ctx, "screenshot", map[string]any{"scale": scale, "format": format}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Data, nil
}
