package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
		if typing {
			// Issue #46: the agent answers before this, with an error that says what it waited
			// for, instead of the host timing out blind on an app that does not answer.
			p["replyMs"] = replyLimit(ctx, limit).Milliseconds()
		}
		return d.call(ctx, op, actionParams(o, p), limit)
	}
	m, err := ask()
	if why := typingNotStarted(err); typing && why != "" && ci.On() {
		// A CI allowance (package ci): the app did not answer accessibility in time (its main
		// thread busy, as on a slow runner bringing up the keyboard, issue #46). In CI it is
		// asked once more; elsewhere the agent's error, which says what it waited for, fails
		// the step. No key was sent: asking once more cannot type twice. A new request (a new
		// token), as the first one has its answer.
		d.note(fmt.Sprintf("%s: the agent gave up before sending any key (%s); asking once more", op, why))
		m, err = ask()
	}
	if err != nil {
		return driver.ActionResult{}, err
	}
	var r struct {
		Result    string `json:"result"`
		Withheld  string `json:"withheld"`
		Cancelled bool   `json:"cancelled"`
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
	return driver.ActionResult{Withheld: r.Withheld, Cancelled: r.Cancelled, Guarded: r.Guarded, Probed: r.Probed, Targeted: r.Targeted, Clicked: r.Clicked, Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: d.seq(r.Seq)}, nil
}

// typingNotStarted is the agent's reason when err says a type or erase gave up before sending
// any key ("" otherwise): it marks only such errors transient. Keys that may have gone are
// never sent again.
func typingNotStarted(err error) string {
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
	return d.action(ctx, "tap", o, p, 0)
}

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "doubleTap", o, map[string]any{"x": x, "y": y}, 0)
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "press", o, map[string]any{"x": x, "y": y, "ms": hold.Milliseconds()}, hold)
}

// How long a resting swipe's finger stays still before it moves and before it lifts (the
// agent's Gestures.swipeRestS and swipeEndRestS; issues #127, #182): the host allows for them
// in the reply limit.
const (
	swipeRest    = 200 * time.Millisecond
	swipeEndRest = time.Second
)

func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	p := map[string]any{"x1": x1, "y1": y1, "x2": x2, "y2": y2, "ms": dur.Milliseconds()}
	if o.Drag {
		p["drag"] = true
	}
	if o.Rest {
		p["rest"] = true
		dur += swipeRest + swipeEndRest
	}
	return d.action(ctx, "swipe", o, p, dur)
}

func (d *Driver) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.action(ctx, "type", o, map[string]any{"text": text}, time.Duration(len(text))*40*time.Millisecond)
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
