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
	"github.com/elforama/testbuddy/internal/ui"
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
	} else if w := o.Watch; w != nil && op == "tap" && w.R > w.L && w.B > w.T {
		// Issue #324: the bounds of a button found by its text, [l,t,r,b] in points: the probe
		// says whether the touch went to the view with those bounds, or one inside it ("reached").
		p["watchFrame"] = []int{w.L, w.T, w.R, w.B}
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
		Result   string `json:"result"`
		Withheld string `json:"withheld"`
		// Issue #39: why the agent's own check withheld the touch at "preflight", and the
		// button's live frame when it found one. An older agent sends neither.
		WithheldWhy string `json:"withheldWhy"`
		LiveFrame   []int  `json:"liveFrame"`
		Cancelled   bool   `json:"cancelled"`
		Touch       string `json:"touch"`
		Guarded     bool   `json:"guarded"`
		Probed      bool   `json:"probed"`
		Targeted    bool   `json:"targeted"`
		// Issue #324: the touch of a tap with watchFrame went to the button's view. An older
		// agent sends none: unknown.
		Reached  bool  `json:"reached"`
		Clicked  *bool `json:"clicked"`
		InjectMs int   `json:"injectMs"`
		SettleMs int   `json:"settleMs"`
		Seq      int64 `json:"seq"`
		// Issue #149: a solid change after the host's read, before the touch went out. An older
		// agent sends none: no change known.
		ChangedSinceRead bool `json:"changedSinceRead"`
		// Issue #111: a swipe whose touches the app never confirmed.
		Unconfirmed bool `json:"unconfirmed"`
		// Issue #334: the token to ask whether the app got a tap's or double tap's touches.
		Delivery int64 `json:"delivery"`
		// Issue #290: the app was still coming to the foreground and the agent waited for it
		// to take input; appActive false: it still had not when the input went.
		AwaitedInputMs *int  `json:"awaitedInputMs"`
		AppActive      *bool `json:"appActive"`
	}
	if err := m.Decode(&r); err != nil {
		return driver.ActionResult{}, err
	}
	d.noteInputWait(op+": the app", r.AwaitedInputMs, r.AppActive)
	if r.Withheld != "" && (op != "tap" || o.Target == nil || !r.Guarded || (r.Withheld != "preflight" && r.Withheld != "delivery" && r.Withheld != "moved") || r.Clicked != nil) {
		return driver.ActionResult{}, fmt.Errorf("invalid tap delivery receipt %q", r.Withheld)
	}
	// Issue #131: a guarded, probed tap's touch that ended cancelled. Never with a click (that
	// touch did act) or a withheld touch (that one never reached the app).
	if r.Cancelled && (op != "tap" || o.Target == nil || !r.Guarded || !r.Probed || r.Withheld != "" || r.Clicked != nil) {
		return driver.ActionResult{}, errors.New("invalid tap delivery receipt: cancelled")
	}
	res := driver.ActionResult{Withheld: r.Withheld, WithheldWhy: r.WithheldWhy, Cancelled: r.Cancelled, Touch: r.Touch, Guarded: r.Guarded, Probed: r.Probed, Targeted: r.Targeted, Clicked: r.Clicked, Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: d.seq(r.Seq)}
	res.ChangedSinceRead = r.ChangedSinceRead && op == "tap" && o.Target != nil
	res.Reached = r.Reached && op == "tap" && o.Target == nil && o.Watch != nil && r.Withheld == "" && !r.Cancelled
	if r.Withheld == "preflight" && len(r.LiveFrame) == 4 {
		res.LiveFrame = &ui.Rect{L: r.LiveFrame[0], T: r.LiveFrame[1], R: r.LiveFrame[2], B: r.LiveFrame[3]}
	}
	res.Unconfirmed = op == "swipe" && r.Unconfirmed
	// Issue #334: a token only for a touch nothing says reached the app (the probe did not see
	// it end, no control reported its action) and that was not withheld or cancelled.
	if (op == "tap" || op == "doubleTap") && r.Delivery > 0 && r.Touch != "ended" && r.Touch != "cancelled" &&
		!r.Targeted && !res.Reached && r.Clicked == nil && r.Withheld == "" && !r.Cancelled {
		res.DeliveryToken = r.Delivery
	}
	// Issue #280: the iOS input to the shared swallowed-tap rule.
	res.AppEntering = op == "tap" && r.AppActive != nil && !*r.AppActive
	return res, nil
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
//
// Only inside a web view (issue #264): the wait cost ~1 s in the navigation scenario and ~2 s
// in animations, on native buttons tapped 0-51 pt apart, where nothing takes two taps as one.
// Measured on an iPhone 17 Pro simulator (iOS 27), bench WKWebView page, pairs sent with no
// spacing: up to 45 pt apart, 150 or 250 ms apart, the page zoomed in every run (and 30 pt
// at 350 ms in 1 of 3); 50 pt or more never; so the radius and the window stay. A pair whose
// first tap opened the web view's screen (a native row) and whose second landed on the page at
// that spot never zoomed (18 of 18, 100-700 ms apart). The page's own changes (its "Taps: N")
// bump seq like a new screen, so a change since the last tap tells nothing. So a tap waits
// when the screen read since the last tap (the step's own, which found the target) has a web
// view or a map (which zooms on a double tap) under the point, or when no read came since
// (unknown). A Safari view's page is a WebView in the SafariViewService window the agent adds
// (iOS 26+), and that whole window counts. Other native double-tap recognizers (zoomable
// scroll views, PDFKit, .onTapGesture(count: 2), text views) are not covered, as before #227.
const (
	doubleTapWindow = 450 * time.Millisecond
	doubleTapNear   = 60 // points
)

// tapAction sends a tap or double tap, after waiting out the double-tap window of the last one
// when it is near it, inside a web view or map.
func (d *Driver) tapAction(ctx context.Context, op string, x, y int, o driver.ActionOpts, p map[string]any) (driver.ActionResult, error) {
	d.tapMu.Lock()
	defer d.tapMu.Unlock()
	if last := d.lastTap; !last.at.IsZero() && !o.InRepeat {
		dx, dy := x-last.x, y-last.y
		if dx*dx+dy*dy <= doubleTapNear*doubleTapNear && d.mayBeWeb(x, y, last.reads) {
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
	// The host's CI mode (review of 3b8b330): the agent tracks a tap's delivery (issue #334)
	// only then, as the host asks for its verdict only then.
	if ci.On() {
		p["ci"] = true
	}
	reads := d.reads.Load()
	res, err := d.action(ctx, op, o, p, 0)
	// Recorded even after an error (the touch may have gone), a cancelled ctx included: that
	// costs at most one wait of doubleTapWindow before a later tap near this one.
	at := time.Now()
	if err == nil {
		// The agent's settle wait came after the touch was confirmed: it counts toward the gap.
		at = at.Add(-time.Duration(res.SettleMs) * time.Millisecond)
	}
	d.lastTap = lastTap{x: x, y: y, at: at, reads: reads}
	return res, err
}

// lastTap is where the last tap or double tap went, and when it was confirmed (zero after a
// long press or swipe, which ends UIKit's tap sequence); reads: the screen reads started
// before it was sent.
type lastTap struct {
	x, y  int
	at    time.Time
	reads int64
}

// screenRead is where a screen the driver read has double-tap takers (web views, maps, a
// Safari view's window), n its place among the reads started (issue #264).
type screenRead struct {
	n     int64
	areas []ui.Rect
}

// safariService is the bundle of the window that shows a Safari view's page (the agent's
// Tree.browserWindow).
const safariService = "com.apple.SafariViewService"

// noteRead keeps where s, read n-th, has double-tap takers, unless a later read has answered
// already.
func (d *Driver) noteRead(n int64, s *ui.Screen) {
	r := &screenRead{n: n}
	for _, w := range s.Windows {
		if w.Package == safariService {
			r.areas = append(r.areas, w.Bounds)
		}
	}
	for _, nd := range s.Nodes {
		if nd.Class == "WebView" || nd.Class == "Map" {
			r.areas = append(r.areas, nd.Bounds)
		}
	}
	for {
		old := d.lastRead.Load()
		if old != nil && old.n > n {
			return
		}
		if d.lastRead.CompareAndSwap(old, r) {
			return
		}
	}
}

// mayBeWeb says whether a tap at (x, y) may land in a web view or map (issue #264): true when
// a read started after the last tap (reads: those started before it) shows one under the
// point, or when none did (unknown). Any such node counts, whatever covers it or whichever
// window it is in: when in doubt the tap waits.
func (d *Driver) mayBeWeb(x, y int, reads int64) bool {
	r := d.lastRead.Load()
	if r == nil || r.n <= reads {
		return true
	}
	for _, a := range r.areas {
		if a.Contains(x, y) {
			return true
		}
	}
	return false
}

// endTaps forgets the last tap: a long press or swipe came after it.
func (d *Driver) endTaps() {
	d.tapMu.Lock()
	d.lastTap = lastTap{}
	d.tapMu.Unlock()
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	defer d.endTaps()
	p := map[string]any{"x": x, "y": y, "ms": hold.Milliseconds()}
	if ci.On() {
		p["ci"] = true // as a tap's (tapAction)
	}
	return d.action(ctx, "press", o, p, hold)
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

// EraseText erases n characters from the end of the focused field (issue #301, the iOS half of
// #289): the agent puts the caret at the end, sends the DELs, reads the field back, puts it right
// once, and fails the step when it still does not hold its old text less the erased end.
func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "erase", o, map[string]any{"n": n}, eraseBudget(n, ci.On()))
}

// eraseBudget: the reply time erasing gets past the op's own (issue #301). The DELs go at 20 ms
// a character; the field is then read until it is right or still for a whole cap (300 ms; 1 s
// in CI mode), for up to 3 caps, and as long again after its one repair, which deletes what is
// left one DEL at a time: each its own record, confirmed (~300 ms), then waited for up to the
// cap. The down arrows that put the caret at the end before the DELs and before the repair are
// records too, one a line the text can take: the agent sends them while its time lasts, and
// this allows for caretLines of them each time (a notes-style text of 100 lines). The agent
// answers before the limit this sets (replyMs) whatever the app does; a field that takes the
// DELs answers at once.
func eraseBudget(n int, ciMode bool) time.Duration {
	perKey := 300 * time.Millisecond
	if ciMode {
		perKey = time.Second
	}
	if n < 0 {
		n = 0
	}
	const record = 300 * time.Millisecond
	return time.Duration(n)*(20*time.Millisecond+record+perKey) + 6*perKey + (2*caretLines+4)*record
}

// caretLines: the down arrows the reply limit allows for each time the iOS agent puts the caret
// at the end (issue #301). The agent stops sending them at its own deadline, failing with
// nothing deleted.
const caretLines = 100

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
		return driver.ActionResult{}, driver.NoSuchKey(fmt.Sprintf("key %q is not supported on iOS", key))
	}
	if code == keyCodes["back"] {
		// The agent refuses it too; nothing is sent.
		return driver.ActionResult{}, driver.NoSuchKey("iOS has no back key")
	}
	res, err := d.action(ctx, "key", o, map[string]any{"code": code}, 0)
	if err != nil || code != keyCodes["home"] {
		return res, err
	}
	// Issue #286: a Home press XCTest refused answered ok, the step passed and the flow failed
	// later, on a misleading assertion. The agent now fails such a press itself; this also
	// checks what it is for: SpringBoard in front. The agent's press returns once it is done, so
	// this answers at once when it worked. Never sent again: a press that may have gone is not
	// repeated.
	if err := d.awaitHome(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// homeWait bounds the wait for SpringBoard to come to the front after a Home press.
const homeWait = 3 * time.Second

// awaitHome fails unless SpringBoard comes to the front within homeWait.
func (d *Driver) awaitHome(ctx context.Context) error {
	m, err := d.call(ctx, "awaitPackage", map[string]any{"pkg": springboard, "timeout": homeWait.Milliseconds()}, homeWait+5*time.Second)
	if err != nil {
		return err
	}
	var r struct {
		Found bool `json:"found"`
	}
	if err := m.Decode(&r); err != nil {
		return err
	}
	if r.Found {
		return nil
	}
	front, _ := d.Foreground(ctx)
	if front == "" {
		front = "another app"
	}
	return fmt.Errorf("the Home press did not take effect: %s is still in front after %s, not the home screen", front, homeWait)
}

const springboard = "com.apple.springboard"

func (d *Driver) HideKeyboard(ctx context.Context) error {
	p := map[string]any{}
	if ci.On() {
		// The agent's own swipes that close the keyboard wait for SpringBoard in CI mode, as a
		// flow's do (issue #137); it cannot read the host's environment.
		p["ci"] = true
	}
	_, err := d.action(ctx, "hideKeyboard", driver.ActionOpts{}, p, 5*time.Second)
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

// Delivery asks the agent whether the app in front got the touches of the tap or double tap
// with this token (issue #334, docs/protocol.md "iOS undelivered input"). The agent answers by
// wait whatever the app does.
func (d *Driver) Delivery(ctx context.Context, token int64, wait time.Duration) (driver.DeliveryVerdict, error) {
	m, err := d.call(ctx, "delivery", map[string]any{"token": token, "timeout": wait.Milliseconds()}, wait+5*time.Second)
	if err != nil {
		return driver.DeliveryVerdict{}, err
	}
	var r struct {
		Verdict  string `json:"verdict"`
		ReportMs *int   `json:"reportMs"`
		AnswerMs []int  `json:"answerMs"`
		Pending  bool   `json:"pending"`
	}
	if err := m.Decode(&r); err != nil {
		return driver.DeliveryVerdict{}, err
	}
	v := driver.DeliveryVerdict{Verdict: driver.Unknown, ReportMs: r.ReportMs, AnswerMs: r.AnswerMs, Pending: r.Pending}
	switch r.Verdict {
	case driver.Delivered, driver.NotDelivered:
		v.Verdict = r.Verdict
	}
	return v, nil
}
