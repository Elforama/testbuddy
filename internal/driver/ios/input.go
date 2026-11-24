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
		p["watchFrame"] = []int{w.L, w.T, w.R, w.B}
	}
	limit := timeout + extra + 15*time.Second
	typing := op == "type" || op == "erase"
	if typing && ci.On() {
		p["ci"] = true
	}
	ask := func() (*wire.Msg, error) {
		if boundedOps[op] {
			p["replyMs"] = replyLimit(ctx, limit).Milliseconds()
		}
		return d.call(ctx, op, actionParams(o, p), limit)
	}
	m, err := ask()
	if why := notStarted(err); boundedOps[op] && why != "" && ci.On() {
		d.note(fmt.Sprintf("%s: the agent gave up before sending anything (%s); asking once more", op, why))
		m, err = ask()
	}
	if err != nil {
		return driver.ActionResult{}, err
	}
	var r struct {
		Result           string `json:"result"`
		Withheld         string `json:"withheld"`
		WithheldWhy      string `json:"withheldWhy"`
		LiveFrame        []int  `json:"liveFrame"`
		Cancelled        bool   `json:"cancelled"`
		Touch            string `json:"touch"`
		Guarded          bool   `json:"guarded"`
		Probed           bool   `json:"probed"`
		Targeted         bool   `json:"targeted"`
		Reached          bool   `json:"reached"`
		Clicked          *bool  `json:"clicked"`
		InjectMs         int    `json:"injectMs"`
		SettleMs         int    `json:"settleMs"`
		Seq              int64  `json:"seq"`
		ChangedSinceRead bool   `json:"changedSinceRead"`
		Unconfirmed      bool   `json:"unconfirmed"`
		Delivery         int64  `json:"delivery"`
		AwaitedInputMs   *int   `json:"awaitedInputMs"`
		AppActive        *bool  `json:"appActive"`
		PressSeenMs      *int   `json:"pressSeenMs"`
		PressMs          int    `json:"pressMs"`
		PressStallMs     int    `json:"pressStallMs"`
	}
	if err := m.Decode(&r); err != nil {
		return driver.ActionResult{}, err
	}
	d.noteInputWait(op+": the app", r.AwaitedInputMs, r.AppActive)
	if r.Withheld != "" && (op != "tap" || o.Target == nil || !r.Guarded || (r.Withheld != "preflight" && r.Withheld != "delivery" && r.Withheld != "moved") || r.Clicked != nil) {
		return driver.ActionResult{}, fmt.Errorf("invalid tap delivery receipt %q", r.Withheld)
	}
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
	if (op == "tap" || op == "doubleTap") && r.Delivery > 0 && r.Touch != "ended" && r.Touch != "cancelled" &&
		!r.Targeted && !res.Reached && r.Clicked == nil && r.Withheld == "" && !r.Cancelled {
		res.DeliveryToken = r.Delivery
	}
	if op == "swipe" && r.Delivery > 0 {
		res.DeliveryToken = r.Delivery
	}
	if (op == "tap" || op == "doubleTap" || op == "press") && r.PressSeenMs != nil && r.PressMs > 0 {
		res.PressCut = &driver.PressCut{SeenMs: *r.PressSeenMs, PressMs: r.PressMs, StallMs: r.PressStallMs}
	}
	res.AppEntering = op == "tap" && r.AppActive != nil && !*r.AppActive
	return res, nil
}

var boundedOps = map[string]bool{"type": true, "erase": true, "key": true, "hideKeyboard": true}

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

const (
	doubleTapWindow = 450 * time.Millisecond
	doubleTapNear   = 60
)

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
	if ci.On() {
		p["ci"] = true
	}
	reads := d.reads.Load()
	res, err := d.action(ctx, op, o, p, 0)
	at := time.Now()
	if err == nil {
		at = at.Add(-time.Duration(res.SettleMs) * time.Millisecond)
	}
	d.lastTap = lastTap{x: x, y: y, at: at, reads: reads}
	return res, err
}

type lastTap struct {
	x, y  int
	at    time.Time
	reads int64
}

type screenRead struct {
	n     int64
	areas []ui.Rect
}

const safariService = "com.apple.SafariViewService"

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

func (d *Driver) endTaps() {
	d.tapMu.Lock()
	d.lastTap = lastTap{}
	d.tapMu.Unlock()
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	defer d.endTaps()
	p := map[string]any{"x": x, "y": y, "ms": hold.Milliseconds()}
	if ci.On() {
		p["ci"] = true
	}
	return d.action(ctx, "press", o, p, hold)
}

const (
	swipeRest     = 200 * time.Millisecond
	swipeEndCreep = 1500 * time.Millisecond
	holdDragDrop  = 300 * time.Millisecond
)

func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	defer d.endTaps()
	p := map[string]any{"x1": x1, "y1": y1, "x2": x2, "y2": y2, "ms": dur.Milliseconds()}
	if o.Drag {
		p["drag"] = true
	}
	if ci.On() {
		p["ci"] = true
	}
	if o.Hold > 0 {
		p["hold"] = o.Hold.Milliseconds()
		return d.action(ctx, "swipe", o, p, o.Hold+dur+holdDragDrop)
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
	return d.action(ctx, "type", o, map[string]any{"text": text}, time.Duration(utf8.RuneCountInString(text))*40*time.Millisecond)
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "erase", o, map[string]any{"n": n}, eraseBudget(n, ci.On()))
}

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

const caretLines = 100

var keyCodes = map[string]int{
	"enter": 66, "backspace": 67, "delete": 67, "home": 3, "lock": 26, "power": 26,
	"volume up": 24, "volume down": 25, "tab": 61, "escape": 111, "back": 4,
}

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
		return driver.ActionResult{}, driver.NoSuchKey("iOS has no back key")
	}
	res, err := d.action(ctx, "key", o, map[string]any{"code": code}, 0)
	if err != nil || code != keyCodes["home"] {
		return res, err
	}
	if err := d.awaitHome(ctx); err != nil {
		return res, err
	}
	return res, nil
}

const homeWait = 3 * time.Second

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
		Implicit bool   `json:"implicit"`
	}
	if err := m.Decode(&r); err != nil {
		return driver.DeliveryVerdict{}, err
	}
	v := driver.DeliveryVerdict{Verdict: driver.Unknown, ReportMs: r.ReportMs, AnswerMs: r.AnswerMs, Pending: r.Pending, Implicit: r.Implicit}
	switch r.Verdict {
	case driver.Delivered, driver.NotDelivered:
		v.Verdict = r.Verdict
	}
	return v, nil
}
