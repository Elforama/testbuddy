package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func (d *Driver) mouse(ctx context.Context, typ string, x, y float64, clicks int, extra map[string]any) error {
	p := map[string]any{"type": typ, "x": x, "y": y, "button": "left", "clickCount": clicks}
	if typ == "mouseMoved" {
		p["button"] = "none"
	}
	if typ == "mousePressed" {
		p["buttons"] = 1
	}
	for k, v := range extra {
		p[k] = v
	}
	return d.page.call(ctx, "Input.dispatchMouseEvent", p, nil)
}

func defaults(o driver.ActionOpts) driver.ActionOpts {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 500
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	return o
}

// act runs an input and then waits (on events) for the page to react.
func (d *Driver) act(ctx context.Context, o driver.ActionOpts, do func() error) (driver.ActionResult, error) {
	o = defaults(o)
	d.mu.Lock()
	since := d.solid
	d.mu.Unlock()
	t0 := time.Now()
	if err := do(); err != nil {
		return driver.ActionResult{}, err
	}
	res := driver.ActionResult{InjectMs: int(time.Since(t0).Milliseconds())}
	if o.React && !o.NoSettle {
		// Only until the page starts reacting: the caller waits for the rest when it next
		// needs a still page.
		t1 := time.Now()
		res.Settle = driver.NoChange
		if d.waitUntil(ctx, time.Duration(o.NoChangeMs)*time.Millisecond, func() bool { return d.solid != since }) {
			res.Settle = driver.Changed
		}
		res.SettleMs = int(time.Since(t1).Milliseconds())
	} else if !o.NoSettle {
		t1 := time.Now()
		r, err := d.Settle(ctx, since, o.NoChangeMs, o.Timeout)
		if err != nil {
			return res, err
		}
		res.Settle, res.SettleMs = r, int(time.Since(t1).Milliseconds())
	}
	res.Seq = d.Seq()
	return res, nil
}

func (d *Driver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		if err := d.mouse(ctx, "mouseMoved", fx, fy, 0, nil); err != nil {
			return err
		}
		if err := d.mouse(ctx, "mousePressed", fx, fy, 1, nil); err != nil {
			return err
		}
		return d.mouse(ctx, "mouseReleased", fx, fy, 1, nil)
	})
}

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		for c := 1; c <= 2; c++ {
			if err := d.mouse(ctx, "mousePressed", fx, fy, c, nil); err != nil {
				return err
			}
			if err := d.mouse(ctx, "mouseReleased", fx, fy, c, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		if err := d.mouse(ctx, "mousePressed", fx, fy, 1, nil); err != nil {
			return err
		}
		select { // the hold is the gesture
		case <-time.After(hold):
		case <-ctx.Done():
			return ctx.Err()
		}
		return d.mouse(ctx, "mouseReleased", fx, fy, 1, nil)
	})
}

// Swipe scrolls what is under the start point by the swipe's distance (a wheel event), the
// way a person scrolls a web page. A drag (ActionOpts.Drag: scrollUntilVisible, clearing a
// cover) scrolls that element itself instead (scrollBy, instant): Chrome may apply a wheel
// event's scroll later than the event, after the screen was read for the next step, and a tap
// then landed on the row that scrolled in under it (bench/clicks.py: "meant Item 30, hit Item
// 34", one wheel step). A point over a frame, or nothing that scrolls there, falls back to
// the wheel.
func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		// A swipe that starts on something made to be dragged (a range slider, a draggable
		// item, a handle whose cursor says so) is a mouse drag: scrolling would not move it.
		var draggable bool
		if err := d.eval(ctx, fmt.Sprintf(dragTargetJS, x1, y1), &draggable); err == nil && draggable {
			return d.mouseDrag(ctx, x1, y1, x2, y2, dur)
		}
		if o.Drag {
			var res string
			expr := fmt.Sprintf(scrollByJS, x1, y1, x1-x2, y1-y2)
			if err := d.eval(ctx, expr, &res); err == nil && res == "moved" {
				return nil
			}
		}
		return d.mouse(ctx, "mouseWheel", float64(x1), float64(y1), 0, map[string]any{
			"deltaX": float64(x1 - x2), "deltaY": float64(y1 - y2), "button": "none"})
	})
}

// dragTargetJS: whether the element under (x, y) is made to be dragged: a range input, a
// slider, a draggable element, or one whose cursor (or an ancestor's, up to 3 levels) offers a
// drag (grab, move, resize).
const dragTargetJS = `(function (x, y) {
  let el = document.elementFromPoint(x, y);
  while (el && el.shadowRoot) { const i = el.shadowRoot.elementFromPoint(x, y); if (!i || i === el) break; el = i; }
  for (let n = el, k = 0; n && n.nodeType === 1 && k < 4; n = n.parentElement, k++) {
    if (n.tagName === 'INPUT' && n.type === 'range') return true;
    const role = (n.getAttribute('role') || '').toLowerCase();
    if (role === 'slider' || n.getAttribute('draggable') === 'true') return true;
    const c = getComputedStyle(n).cursor;
    if (/^(grab|grabbing|move|[nsewc]+-resize|ew-resize|ns-resize|col-resize|row-resize)$/.test(c)) return true;
  }
  return false;
})(%d, %d)`

// mouseDrag presses at (x1, y1), moves to (x2, y2) in steps over dur (at least 200 ms), and
// releases there.
func (d *Driver) mouseDrag(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration) error {
	if dur < 200*time.Millisecond {
		dur = 200 * time.Millisecond
	}
	const steps = 12
	if err := d.mouse(ctx, "mouseMoved", float64(x1), float64(y1), 0, nil); err != nil {
		return err
	}
	if err := d.mouse(ctx, "mousePressed", float64(x1), float64(y1), 1, nil); err != nil {
		return err
	}
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dur / steps):
		}
		x := float64(x1) + float64(x2-x1)*float64(i)/steps
		y := float64(y1) + float64(y2-y1)*float64(i)/steps
		if err := d.mouse(ctx, "mouseMoved", x, y, 0, map[string]any{"button": "left", "buttons": 1}); err != nil {
			return err
		}
	}
	return d.mouse(ctx, "mouseReleased", float64(x2), float64(y2), 1, nil)
}

// scrollByJS scrolls the nearest scrollable element under (x, y) (else the page) by (dx, dy)
// at once. "moved", "still" (nothing scrolled) or "frame" (the point is over a frame).
const scrollByJS = `(function (x, y, dx, dy) {
  const at = document.elementFromPoint(x, y);
  if (!at) return 'still';
  if (at.tagName === 'IFRAME' || at.tagName === 'FRAME') return 'frame';
  const scrolls = (el) => {
    const s = getComputedStyle(el);
    return (dy !== 0 && /(auto|scroll|overlay)/.test(s.overflowY) && el.scrollHeight > el.clientHeight) ||
      (dx !== 0 && /(auto|scroll|overlay)/.test(s.overflowX) && el.scrollWidth > el.clientWidth);
  };
  let el = at;
  while (el && el !== document.body && el !== document.documentElement && !scrolls(el)) el = el.parentElement;
  const t = (!el || el === document.body || el === document.documentElement) ? (document.scrollingElement || document.documentElement) : el;
  const before = t.scrollLeft + ',' + t.scrollTop;
  if (typeof window !== 'undefined' && window.__tb && window.__tb.acted) window.__tb.acted();
  t.scrollBy({ left: dx, top: dy, behavior: 'instant' });
  return t.scrollLeft + ',' + t.scrollTop !== before ? 'moved' : 'still';
})(%d, %d, %d, %d)`

type keyDef struct {
	key, code string
	vk        int
	text      string
}

var keys = map[string]keyDef{
	"enter":     {"Enter", "Enter", 13, "\r"},
	"backspace": {"Backspace", "Backspace", 8, ""},
	"tab":       {"Tab", "Tab", 9, ""},
	"escape":    {"Escape", "Escape", 27, ""},
	"home":      {"Home", "Home", 36, ""},
}

func (d *Driver) key(ctx context.Context, k keyDef) error {
	down := map[string]any{"type": "keyDown", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk}
	if k.text != "" {
		down["text"] = k.text
	} else {
		down["type"] = "rawKeyDown"
	}
	if err := d.page.call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return d.page.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.vk}, nil)
}

func (d *Driver) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.act(ctx, o, func() error {
		for _, r := range text {
			s := string(r)
			if r == '\n' {
				if err := d.key(ctx, keys["enter"]); err != nil {
					return err
				}
				continue
			}
			if err := d.page.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyDown", "text": s, "unmodifiedText": s, "key": s}, nil); err != nil {
				return err
			}
			if err := d.page.call(ctx, "Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": s}, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.act(ctx, o, func() error {
		// No more presses than the field holds.
		length := -1 // unknown if the probe fails: press all n
		d.eval(ctx, `(() => { const e = document.activeElement; return e && 'value' in e ? e.value.length : (e && e.isContentEditable ? e.innerText.length : -1) })()`, &length)
		if length >= 0 && length < n {
			n = length
		}
		for i := 0; i < n; i++ {
			if err := d.key(ctx, keys["backspace"]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (d *Driver) PressKey(ctx context.Context, key string, o driver.ActionOpts) (driver.ActionResult, error) {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "back" {
		return d.act(ctx, o, func() error { return d.back(ctx) })
	}
	def, ok := keys[k]
	if !ok {
		return driver.ActionResult{}, fmt.Errorf("key %q is not supported on web (Enter, Backspace, Tab, Escape, Home, Back)", key)
	}
	return d.act(ctx, o, func() error { return d.key(ctx, def) })
}

func (d *Driver) back(ctx context.Context) error {
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := d.page.call(ctx, "Page.getNavigationHistory", nil, &h); err != nil {
		return err
	}
	if h.CurrentIndex <= 0 {
		return nil
	}
	return d.page.call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID}, nil)
}

func (d *Driver) HideKeyboard(ctx context.Context) error { return nil }

func (d *Driver) Screenshot(ctx context.Context, scale float64, format string) ([]byte, error) {
	if format == "" {
		format = "png"
	}
	w, h := d.Size()
	p := map[string]any{"format": format}
	if format == "jpeg" {
		p["quality"] = 80
	}
	if scale > 0 && scale < 1 {
		p["clip"] = map[string]any{"x": 0, "y": 0, "width": w, "height": h, "scale": scale}
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := d.page.call(ctx, "Page.captureScreenshot", p, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}
