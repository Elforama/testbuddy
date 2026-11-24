package android

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
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
	if o.Aim != nil {
		p["aim"] = map[string]any{"read": o.Aim.Read, "window": o.Aim.Window, "ref": o.Aim.Ref}
	}
	return p
}

func (d *Driver) Clicks(ctx context.Context) (int64, error) {
	m, err := d.call(ctx, "clicks", nil, 5*time.Second)
	if err != nil {
		return 0, err
	}
	var r struct {
		Clicks int64 `json:"clicks"`
	}
	if err := m.Decode(&r); err != nil {
		return 0, err
	}
	return r.Clicks, nil
}

func (d *Driver) action(ctx context.Context, op string, o driver.ActionOpts, p map[string]any, extra time.Duration) (driver.ActionResult, error) {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	m, c, err := d.callOn(ctx, op, actionParams(o, p), timeout+extra+10*time.Second)
	if err != nil {
		return driver.ActionResult{}, err
	}
	var r struct {
		VerifyUnavailable bool   `json:"verifyUnavailable"`
		Result            string `json:"result"`
		InjectMs          int    `json:"injectMs"`
		SettleMs          int    `json:"settleMs"`
		Seq               int64  `json:"seq"`
		Clicked           *bool  `json:"clicked"`
		Before            *int64 `json:"clicksBefore"`
		NotInjected       string `json:"notInjected"`
		WindowWaitMs      int64  `json:"windowWaitMs"`
		FirstInWindow     *bool  `json:"firstInWindow"`
		WindowAgeMs       *int64 `json:"windowAgeMs"`
		WindowOver        bool   `json:"windowOver"`
		Typing            string `json:"typing"`
		AimAt             []int  `json:"aimAt"`
		AimMs             int    `json:"aimMs"`
	}
	m.Decode(&r)
	if r.VerifyUnavailable {
		return driver.ActionResult{}, driver.ErrTypingVerificationUnavailable
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[tb] %s %v: inject %dms, settle %s %dms, seq %d, aim %v read in %dms, firstInWindow %v age %v over %v\n",
			op, p, r.InjectMs, r.Result, r.SettleMs, r.Seq, r.AimAt, r.AimMs, ptrStr(r.FirstInWindow), ptrStr(r.WindowAgeMs), r.WindowOver)
	}
	return driver.ActionResult{Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: d.seqOn(c, r.Seq),
		Clicked: r.Clicked, ClicksBefore: r.Before, NotInjected: r.NotInjected, WindowWait: time.Duration(r.WindowWaitMs) * time.Millisecond,
		Typing: r.Typing, FirstInWindow: r.FirstInWindow != nil && *r.FirstInWindow && (op == "tap" || op == "doubleTap" || op == "press"),
		WindowAge: windowAge(r.WindowAgeMs), WindowOver: r.WindowOver, AimAt: aimAt(r.AimAt)}, nil
}

func aimAt(b []int) *ui.Rect {
	if len(b) != 4 {
		return nil
	}
	return &ui.Rect{L: b[0], T: b[1], R: b[2], B: b[3]}
}

func windowAge(ms *int64) time.Duration {
	if ms == nil || *ms < 0 {
		return 0
	}
	return time.Duration(*ms) * time.Millisecond
}

var debug = os.Getenv("TB_DEBUG") != ""

func ptrStr[T any](p *T) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func touchAt(x, y int) map[string]any {
	p := map[string]any{"x": x, "y": y}
	if ci.On() {
		p["ci"] = true
	}
	return p
}

func (d *Driver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "tap", o, touchAt(x, y), 0)
}

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "doubleTap", o, touchAt(x, y), 0)
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	p := touchAt(x, y)
	p["ms"] = hold.Milliseconds()
	return d.action(ctx, "press", o, p, hold)
}

const holdDragDrop = 300 * time.Millisecond

func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	p := map[string]any{"x1": x1, "y1": y1, "x2": x2, "y2": y2, "ms": dur.Milliseconds()}
	if o.Hold > 0 {
		p["hold"] = o.Hold.Milliseconds()
		return d.action(ctx, "swipe", o, p, o.Hold+dur+holdDragDrop)
	}
	if o.Drag {
		p["drag"] = true
	}
	return d.action(ctx, "swipe", o, p, dur)
}

func (d *Driver) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.action(ctx, "type", o, map[string]any{"text": text}, time.Duration(len(text))*20*time.Millisecond)
}

func (d *Driver) TypeTextChecked(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	p := map[string]any{"text": text, "verifyFirst": true}
	if ci.On() {
		p["ci"] = true
	}
	return d.action(ctx, "type", o, p, checkedTypingBudget(text, ci.On()))
}

func checkedTypingBudget(text string, ciMode bool) time.Duration {
	perKey := 300 * time.Millisecond
	if ciMode {
		perKey = time.Second
	}
	n := time.Duration(utf8.RuneCountInString(text))
	return 8*time.Second + n*5*time.Second + (5*n+19)*perKey
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	p := map[string]any{"n": n}
	if ci.On() {
		p["ci"] = true
	}
	return d.action(ctx, "erase", o, p, eraseBudget(n, ci.On()))
}

func eraseBudget(n int, ciMode bool) time.Duration {
	perKey := 300 * time.Millisecond
	if ciMode {
		perKey = time.Second
	}
	if n < 0 {
		n = 0
	}
	return time.Duration(n)*20*time.Millisecond + time.Duration(n+6)*perKey
}

var keyCodes = map[string]int{
	"enter": 66, "backspace": 67, "back": 4, "home": 3, "lock": 26, "power": 26,
	"volume up": 24, "volume down": 25, "tab": 61, "escape": 111,
	"remote dpad up": 19, "remote dpad down": 20, "remote dpad left": 21, "remote dpad right": 22,
	"remote dpad center": 23, "remote media play pause": 85, "remote media stop": 86,
	"remote media next": 87, "remote media previous": 88, "remote media rewind": 89,
	"remote media fast forward": 90, "remote system navigation up": 280, "remote system navigation down": 281,
	"remote button a": 96, "remote button b": 97, "remote menu": 82, "remote tv input": 178,
	"remote tv input hdmi 1": 243, "remote tv input hdmi 2": 244, "remote tv input hdmi 3": 245,
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
		return driver.ActionResult{}, fmt.Errorf("unknown key %q", key)
	}
	if code == 67 {
		if result, handled, err := d.editorBackspace(ctx, o); handled || err != nil {
			return result, err
		}
	}
	return d.action(ctx, "key", o, map[string]any{"code": code}, 0)
}

func (d *Driver) HideKeyboard(ctx context.Context) error {
	pending := 1000
	if ci.On() {
		pending = 2500
	}
	m, err := d.call(ctx, "hideKeyboard", map[string]any{"timeout": 3000, "pendingMs": pending}, 15*time.Second)
	if err != nil {
		return err
	}
	var reply struct {
		Hidden bool `json:"hidden"`
		Late   bool `json:"late"`
	}
	if err := m.Decode(&reply); err != nil {
		return err
	}
	if reply.Late && !reply.Hidden {
		return fmt.Errorf("the Android keyboard came up late, after hideKeyboard began, and did not close within 3s")
	}
	if reply.Late {
		d.note("the keyboard came up after hideKeyboard; closed it")
	}
	if !reply.Hidden {
		return fmt.Errorf("the Android keyboard did not close within 3s")
	}
	return nil
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

func (d *Driver) Sh(ctx context.Context, cmd string) (string, int, error) {
	m, err := d.call(ctx, "sh", map[string]any{"cmd": cmd, "timeout": 60000}, 65*time.Second)
	if err != nil {
		return "", -1, err
	}
	var r struct {
		Code int    `json:"code"`
		Out  string `json:"out"`
	}
	m.Decode(&r)
	return r.Out, r.Code, nil
}

func (d *Driver) shOK(ctx context.Context, cmd string) (string, error) {
	out, code, err := d.Sh(ctx, cmd)
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("%s: exit %d: %s", cmd, code, strings.TrimSpace(out))
	}
	return out, nil
}
