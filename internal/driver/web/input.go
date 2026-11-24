package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func (d *Driver) act(ctx context.Context, o driver.ActionOpts, do func() error) (driver.ActionResult, error) {
	return d.actQuiet(ctx, o, quietWindow, do, nil)
}

func (d *Driver) actQuiet(ctx context.Context, o driver.ActionOpts, quiet time.Duration, do func() error, reacted func() bool) (driver.ActionResult, error) {
	o = defaults(o)
	d.mu.Lock()
	since := d.solid
	d.mu.Unlock()
	d.actStarted(quiet)
	t0 := time.Now()
	if err := do(); err != nil {
		return driver.ActionResult{}, err
	}
	res := driver.ActionResult{InjectMs: int(time.Since(t0).Milliseconds())}
	if reacted != nil && !o.NoSettle {
		d.mu.Lock()
		none := d.solid == since
		d.mu.Unlock()
		if none && reacted() {
			d.mu.Lock()
			d.quietUntil = time.Now().Add(focusQuietFloor)
			d.mu.Unlock()
			d.netChanged()
		}
	}
	if o.React && !o.NoSettle {
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
	return d.actQuiet(ctx, o, quietWindow, func() error {
		fx, fy := float64(x), float64(y)
		d.awaitFramesDrawn(ctx, x, y)
		if err := d.mouse(ctx, "mouseMoved", fx, fy, 0, nil); err != nil {
			return err
		}
		if err := d.mouse(ctx, "mousePressed", fx, fy, 1, nil); err != nil {
			return err
		}
		return d.mouse(ctx, "mouseReleased", fx, fy, 1, nil)
	}, func() bool { return d.focusKept(ctx, x, y) })
}

func (d *Driver) focusKept(ctx context.Context, x, y int) bool {
	var kept bool
	return d.eval(ctx, fmt.Sprintf(focusKeptJS, x, y), &kept) == nil && kept
}

const focusKeptJS = `(function (x, y) {
  let at = document.elementFromPoint(x, y);
  while (at && at.shadowRoot) { const i = at.shadowRoot.elementFromPoint(x, y); if (!i || i === at) break; at = i; }
  let a = document.activeElement;
  while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement;
  if (!at || !a || a === document.body || a === document.documentElement) return false;
  const frame = (n) => n.tagName === 'IFRAME' || n.tagName === 'FRAME';
  if (frame(a) || frame(at)) return false;
  // Focus inside a closed shadow root shows as its host, with nothing to tell what has it
  // (a button, perhaps): a host that cannot take focus itself, or any custom element whose
  // root (if any) is closed (its role may be set through ElementInternals), counts as a control.
  const ownFocus = (n) => n.hasAttribute('tabindex') || n.isContentEditable ||
    n.matches('a[href],area[href],button,input,select,textarea,summary');
  if (!a.shadowRoot && (!ownFocus(a) || a.localName.includes('-'))) return false;
  const control = 'a[href],button,input,select,textarea,label,summary,[onclick],[role=button],[role=link],' +
    '[role=checkbox],[role=switch],[role=radio],[role=tab],[role=menuitem],[role=option],[role=combobox]';
  const field = (n) => n.isContentEditable || n.tagName === 'TEXTAREA' ||
    (n.tagName === 'INPUT' && !/^(button|submit|reset|checkbox|radio|range|file|color|image|hidden)$/i.test(n.type));
  const up = (n) => n.parentElement || (n.getRootNode && n.getRootNode().host) || null;
  for (let n = at; n; n = up(n)) {
    if (n === a) return field(n) || !n.matches(control);
    if (n.nodeType === 1 && n.matches(control)) return false;
  }
  return false;
})(%d, %d)`

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		fx, fy := float64(x), float64(y)
		d.awaitFramesDrawn(ctx, x, y)
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
		d.awaitFramesDrawn(ctx, x, y)
		if err := d.mouse(ctx, "mousePressed", fx, fy, 1, nil); err != nil {
			return err
		}
		select {
		case <-time.After(hold):
		case <-ctx.Done():
			return ctx.Err()
		}
		return d.mouse(ctx, "mouseReleased", fx, fy, 1, nil)
	})
}

func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.act(ctx, o, func() error {
		var draggable bool
		if err := d.eval(ctx, fmt.Sprintf(dragTargetJS, x1, y1), &draggable); err == nil && draggable {
			return d.mouseDrag(ctx, x1, y1, x2, y2, dur)
		}
		var frames []frameAt
		if o.Drag {
			var res string
			err := d.eval(ctx, fmt.Sprintf(scrollByJS, x1, y1, x1-x2, y1-y2), &res)
			if err == nil && res == "moved" {
				return nil
			}
			if err == nil && res == "frame" {
				frames = d.frameUnder(ctx, x1, y1)
				for i := len(frames) - 1; i >= 0; i-- {
					f := frames[i]
					if err := d.evalIn(ctx, f.session, fmt.Sprintf(scrollByJS, f.x, f.y, x1-x2, y1-y2), &res); err == nil && res == "moved" {
						return nil
					}
				}
			}
		} else {
			frames = d.frameUnder(ctx, x1, y1)
		}
		for _, f := range frames {
			d.awaitDrawn(ctx, f.session)
		}
		return d.mouse(ctx, "mouseWheel", float64(x1), float64(y1), 0, map[string]any{
			"deltaX": float64(x1 - x2), "deltaY": float64(y1 - y2), "button": "none"})
	})
}

type frameAt struct {
	session string
	x, y    int
}

func (d *Driver) frameUnder(ctx context.Context, x, y int) []frameAt {
	d.mu.Lock()
	cur, has := d.curSessionLocked(), false
	for _, f := range d.frames {
		if f.tab == cur {
			has = true
			break
		}
	}
	d.mu.Unlock()
	if !has {
		return nil
	}
	var out []frameAt
	session := ""
	for depth := 0; depth < 8; depth++ {
		var r struct {
			Result struct {
				ObjectID string `json:"objectId"`
			} `json:"result"`
		}
		expr := fmt.Sprintf("window.__tb && window.__tb.frameAt ? window.__tb.frameAt(%d, %d) : null", x, y)
		if err := d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": expr}, &r); err != nil || r.Result.ObjectID == "" {
			break
		}
		var node struct {
			Node struct {
				FrameID string `json:"frameId"`
			} `json:"node"`
		}
		err := d.page.callIn(ctx, session, "DOM.describeNode", map[string]any{"objectId": r.Result.ObjectID}, &node)
		var origin [2]int
		oerr := d.evalIn(ctx, session, "window.__tb.frameOrigin", &origin)
		d.page.callIn(ctx, session, "Runtime.releaseObject", map[string]any{"objectId": r.Result.ObjectID}, nil)
		if err != nil || oerr != nil || node.Node.FrameID == "" {
			break
		}
		d.mu.Lock()
		next := ""
		for id, f := range d.frames {
			if f.target == node.Node.FrameID {
				next = id
				break
			}
		}
		d.mu.Unlock()
		if next == "" {
			break
		}
		x, y, session = x-origin[0], y-origin[1], next
		out = append(out, frameAt{session: session, x: x, y: y})
	}
	return out
}

func (d *Driver) evalIn(ctx context.Context, session, expr string, out any) error {
	if session == "" {
		return d.eval(ctx, expr, out)
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}, &r); err != nil {
		return err
	}
	if r.Exception != nil {
		return fmt.Errorf("frame script: %s", r.Exception.Text)
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

func (d *Driver) awaitFramesDrawn(ctx context.Context, x, y int) {
	d.mu.Lock()
	cur, undrawn := d.curSessionLocked(), false
	for _, f := range d.frames {
		if f.tab == cur && !f.drawn {
			undrawn = true
			break
		}
	}
	d.mu.Unlock()
	if !undrawn {
		return
	}
	for _, f := range d.frameUnder(ctx, x, y) {
		d.awaitDrawn(ctx, f.session)
	}
}

const drawnWait = 250 * time.Millisecond

func (d *Driver) awaitDrawn(ctx context.Context, session string) {
	d.mu.Lock()
	f := d.frames[session]
	drawn := f != nil && f.drawn
	d.mu.Unlock()
	if drawn {
		return
	}
	const js = `new Promise((ok) => { const t = setTimeout(() => ok('late'), 250);
  requestAnimationFrame(() => requestAnimationFrame(() => { clearTimeout(t); ok('drawn'); })); })`
	wctx, cancel := context.WithTimeout(ctx, drawnWait+50*time.Millisecond)
	defer cancel()
	var res string
	if err := d.evalIn(wctx, session, js, &res); err == nil && res == "drawn" {
		d.mu.Lock()
		if f := d.frames[session]; f != nil {
			f.drawn = true
		}
		d.mu.Unlock()
	}
}

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

func (d *Driver) mouseDrag(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration) error {
	if dur < 200*time.Millisecond {
		dur = 200 * time.Millisecond
	}
	const steps = 12
	d.awaitFramesDrawn(ctx, x1, y1)
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

const scrollByJS = `(function (x, y, dx, dy) {
  let doc = document, at = doc.elementFromPoint(x, y);
  const style = (el) => (doc.defaultView && doc.defaultView.getComputedStyle ? doc.defaultView.getComputedStyle(el) : getComputedStyle(el));
  for (let k = 0; at && k < 32; k++) {
    if (at.shadowRoot) {
      const inner = at.shadowRoot.elementFromPoint(x, y);
      if (inner && inner !== at) { at = inner; continue; }
    }
    if (at.tagName === 'IFRAME' || at.tagName === 'FRAME') {
      let inner = null;
      try { inner = at.contentDocument; } catch (e) {}
      if (!inner || !inner.documentElement) return 'frame';
      const r = at.getBoundingClientRect(), cs = style(at);
      x -= r.x + (parseFloat(cs.borderLeftWidth) || 0) + (parseFloat(cs.paddingLeft) || 0);
      y -= r.y + (parseFloat(cs.borderTopWidth) || 0) + (parseFloat(cs.paddingTop) || 0);
      doc = inner;
      at = doc.elementFromPoint(x, y);
      continue;
    }
    break;
  }
  if (!at) return 'still';
  const scrolls = (el) => {
    const s = style(el);
    return (dy !== 0 && /(auto|scroll|overlay)/.test(s.overflowY) && el.scrollHeight > el.clientHeight) ||
      (dx !== 0 && /(auto|scroll|overlay)/.test(s.overflowX) && el.scrollWidth > el.clientWidth);
  };
  // Up to the nearest scrolling ancestor, out of shadow roots through their hosts.
  const up = (el) => el.parentElement || (el.getRootNode && el.getRootNode().host) || null;
  let el = at;
  while (el && el !== doc.body && el !== doc.documentElement && !scrolls(el)) el = up(el);
  const t = (!el || el === doc.body || el === doc.documentElement) ? (doc.scrollingElement || doc.documentElement) : el;
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
	return d.actQuiet(ctx, o, typingQuietWindow, func() error {
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
	}, nil)
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.actQuiet(ctx, o, typingQuietWindow, func() error {
		probe := struct {
			Len    int  `json:"len"`
			Placed bool `json:"placed"`
		}{Len: -1}
		d.eval(ctx, eraseProbe, &probe)
		if probe.Len > 0 && !probe.Placed {
			if err := d.key(ctx, endKey); err != nil {
				return err
			}
		}
		length := probe.Len
		if length >= 0 && length < n {
			n = length
		}
		for i := 0; i < n; i++ {
			if err := d.key(ctx, keys["backspace"]); err != nil {
				return err
			}
		}
		return nil
	}, nil)
}

const eraseProbe = `(() => {
  const e = document.activeElement;
  if (e && 'value' in e) {
    const len = e.value.length;
    try { e.setSelectionRange(len, len); return { len, placed: e.selectionStart === len }; } catch (_) { return { len, placed: false }; }
  }
  if (e && e.isContentEditable) {
    const r = document.createRange(); r.selectNodeContents(e); r.collapse(false);
    const s = window.getSelection(); s.removeAllRanges(); s.addRange(r);
    return { len: e.innerText.length, placed: true };
  }
  return { len: -1, placed: false };
})()`

var endKey = keyDef{"End", "End", 35, ""}

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
	return d.capture(ctx, p)
}

var screenshotTimeout = 20 * time.Second

func (d *Driver) capture(ctx context.Context, params map[string]any) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, screenshotTimeout)
	defer cancel()
	var r struct {
		Data string `json:"data"`
	}
	if err := d.page.call(c, "Page.captureScreenshot", params, &r); err != nil {
		if ctx.Err() == nil && c.Err() != nil {
			return nil, fmt.Errorf("Page.captureScreenshot: the browser did not answer within %s: the page is not drawing (a window too small or too large for Chrome, a script that never yields, or a stalled compositor or GPU): %w", screenshotTimeout, c.Err())
		}
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}
